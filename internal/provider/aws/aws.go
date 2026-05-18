// Package aws implements the AWS EC2 provider. Lifecycle is driven by the
// `aws ec2` CLI (no SDK lock-in). The provider tags every created instance
// with `vmlab=<name>` so a single Name filter locates it on subsequent calls.
//
// Defaults are tuned for "smoke test a Linux box, then terminate it": the
// suggested disposition for new instances is destroy on success.
package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/target"
)

// Provider implements provider.Provider for AWS EC2.
type Provider struct{}

// New returns an AWS provider.
func New() *Provider { return &Provider{} }

// Name reports the provider name.
func (p *Provider) Name() string { return "aws" }

// Doctor checks the aws CLI + a reachable caller identity.
func (p *Provider) Doctor(ctx context.Context, i provider.Instance) provider.Health {
	if _, err := exec.LookPath("aws"); err != nil {
		return provider.Health{OK: false, Message: "aws CLI not on PATH (install: brew install awscli)"}
	}
	out, err := p.run(ctx, i, "sts", "get-caller-identity", "--output", "json")
	if err != nil {
		return provider.Health{OK: false, Message: "aws sts get-caller-identity: " + err.Error()}
	}
	var id struct {
		Account string `json:"Account"`
		Arn     string `json:"Arn"`
	}
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		return provider.Health{OK: false, Message: err.Error()}
	}
	return provider.Health{OK: true, Message: fmt.Sprintf("account=%s arn=%s", id.Account, id.Arn)}
}

// Status returns the EC2 instance's current state. Locates by the vmlab tag
// rather than by ID so a single instance YAML can survive recreation cycles.
func (p *Provider) Status(ctx context.Context, i provider.Instance) (provider.State, error) {
	if i.Name == "" {
		return provider.StateUnknown, errors.New("instance name required")
	}
	id, state, err := p.findByTag(ctx, i)
	if err != nil {
		return provider.StateUnknown, err
	}
	if id == "" {
		return provider.StateNotFound, nil
	}
	switch state {
	case "running":
		return provider.StateRunning, nil
	case "pending":
		return provider.StateStarting, nil
	case "stopped", "stopping", "shutting-down":
		return provider.StateStopped, nil
	case "terminated":
		return provider.StateNotFound, nil
	}
	return provider.StateUnknown, nil
}

// findByTag returns (instanceId, state) for the EC2 instance tagged with
// vmlab=<i.Name>. Empty ID means nothing matched (StateNotFound).
func (p *Provider) findByTag(ctx context.Context, i provider.Instance) (string, string, error) {
	out, err := p.run(ctx, i, "ec2", "describe-instances",
		"--filters", "Name=tag:vmlab,Values="+i.Name,
		"--query", "Reservations[].Instances[].{Id:InstanceId,State:State.Name,PublicIp:PublicIpAddress}",
		"--output", "json")
	if err != nil {
		return "", "", err
	}
	var list []struct {
		Id, State, PublicIp string
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &list); err != nil {
		return "", "", fmt.Errorf("parse describe-instances: %w", err)
	}
	for _, r := range list {
		if r.State == "terminated" {
			continue
		}
		return r.Id, r.State, nil
	}
	return "", "", nil
}

// Up creates (or starts) the EC2 instance and waits for SSH on its public IP.
func (p *Provider) Up(ctx context.Context, i provider.Instance) (target.Target, provider.EnsureResult, error) {
	res := provider.EnsureResult{}
	prior, err := p.Status(ctx, i)
	if err != nil {
		return target.Target{}, res, err
	}
	res.PriorState = prior
	switch prior {
	case provider.StateRunning, provider.StateStarting:
		res.Reason = "already running"
	case provider.StateNotFound:
		if err := p.create(ctx, i); err != nil {
			return target.Target{}, res, fmt.Errorf("aws run-instances: %w", err)
		}
		res.Changed = true
		res.Reason = "created by vmlab"
	case provider.StateStopped:
		id, _, err := p.findByTag(ctx, i)
		if err != nil {
			return target.Target{}, res, err
		}
		if _, err := p.run(ctx, i, "ec2", "start-instances", "--instance-ids", id); err != nil {
			return target.Target{}, res, fmt.Errorf("aws start-instances: %w", err)
		}
		res.Changed = true
		res.Reason = "started by vmlab"
	default:
		return target.Target{}, res, fmt.Errorf("unexpected prior state: %s", prior)
	}
	ip, err := p.publicIP(ctx, i)
	if err != nil {
		return target.Target{}, res, err
	}
	if err := waitForTCP(ctx, ip, 22, readyTimeout(i)); err != nil {
		return target.Target{}, res, fmt.Errorf("waitForTCP %s:22: %w", ip, err)
	}
	return buildTarget(i, ip), res, nil
}

// Down disposes per Dispose. Suspend → stop-instances (EBS preserved, no
// compute charges); destroy → terminate-instances; poweroff also maps to
// stop-instances since EC2 has no separate halt vs deallocate distinction.
func (p *Provider) Down(ctx context.Context, i provider.Instance, d provider.Dispose) error {
	if d == provider.DisposeKeep {
		return nil
	}
	id, _, err := p.findByTag(ctx, i)
	if err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	switch d {
	case provider.DisposeSuspend, provider.DisposePowerOff:
		_, err := p.run(ctx, i, "ec2", "stop-instances", "--instance-ids", id)
		return err
	case provider.DisposeDestroy:
		_, err := p.run(ctx, i, "ec2", "terminate-instances", "--instance-ids", id)
		return err
	}
	return fmt.Errorf("unknown dispose: %v", d)
}

// create issues `aws ec2 run-instances` with the instance's config and tags
// the new instance with vmlab=<name> for subsequent lookups.
func (p *Provider) create(ctx context.Context, i provider.Instance) error {
	args := []string{"ec2", "run-instances",
		"--image-id", mustSetting(i, "imageId"),
		"--instance-type", settingOr(i, "instanceType", "t4g.nano"),
		"--count", "1",
		"--tag-specifications",
		fmt.Sprintf(`ResourceType=instance,Tags=[{Key=vmlab,Value=%s},{Key=Name,Value=%s}]`, i.Name, i.Name),
	}
	if k := i.SettingString("aws", "keyName"); k != "" {
		args = append(args, "--key-name", k)
	}
	if sg := i.SettingString("aws", "securityGroupIds"); sg != "" {
		args = append(args, "--security-group-ids")
		args = append(args, strings.Split(sg, ",")...)
	}
	if sn := i.SettingString("aws", "subnetId"); sn != "" {
		args = append(args, "--subnet-id", sn)
	}
	if ud := i.SettingString("aws", "userDataFile"); ud != "" {
		args = append(args, "--user-data", "file://"+ud)
	}
	args = append(args, "--output", "json")
	_, err := p.run(ctx, i, args...)
	return err
}

// publicIP fetches the first non-empty public IP among matching instances.
func (p *Provider) publicIP(ctx context.Context, i provider.Instance) (string, error) {
	out, err := p.run(ctx, i, "ec2", "describe-instances",
		"--filters", "Name=tag:vmlab,Values="+i.Name, "Name=instance-state-name,Values=running,pending",
		"--query", "Reservations[].Instances[].PublicIpAddress",
		"--output", "json")
	if err != nil {
		return "", err
	}
	var ips []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &ips); err != nil {
		return "", err
	}
	for _, ip := range ips {
		if ip != "" {
			return ip, nil
		}
	}
	return "", errors.New("aws: instance has no public IP")
}

// run shells out to aws with --region/--profile honoured from instance
// settings. Combines stdout+stderr for diagnostic parsing.
func (p *Provider) run(ctx context.Context, i provider.Instance, args ...string) (string, error) {
	prefix := []string{}
	if r := i.SettingString("aws", "region"); r != "" {
		prefix = append(prefix, "--region", r)
	}
	if pr := i.SettingString("aws", "profile"); pr != "" {
		prefix = append(prefix, "--profile", pr)
	}
	args = append(prefix, args...)
	cmd := exec.CommandContext(ctx, "aws", args...)
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, fmt.Errorf("aws exit=%d: %s", ee.ExitCode(), strings.TrimSpace(out))
		}
		return out, err
	}
	return out, nil
}

func mustSetting(i provider.Instance, key string) string {
	v := i.SettingString("aws", key)
	if v == "" {
		// Return empty; create() will let aws CLI emit a usable error.
		return ""
	}
	return v
}

func settingOr(i provider.Instance, key, fallback string) string {
	if v := i.SettingString("aws", key); v != "" {
		return v
	}
	return fallback
}

func readyTimeout(i provider.Instance) time.Duration {
	if i.Ready.Timeout != "" {
		if d, err := time.ParseDuration(i.Ready.Timeout); err == nil {
			return d
		}
	}
	return 240 * time.Second
}

func buildTarget(i provider.Instance, ip string) target.Target {
	tt := i.Target.Transport
	if tt == "" {
		tt = "ssh"
	}
	user := settingOr(i, "user", "ec2-user")
	settings := map[string]any{
		"ssh": map[string]any{
			"host":       ip,
			"user":       user,
			"strictHost": "accept-new",
		},
	}
	if id := i.SettingString("ssh", "identity"); id != "" {
		settings["ssh"].(map[string]any)["identity"] = id
	}
	return target.Target{
		Name:      i.Name,
		Transport: tt,
		Tags:      i.Tags,
		Settings:  settings,
	}
}

// WaitReady polls TCP :22 on the public IP. Implements provider.ReadyWaiter.
func (p *Provider) WaitReady(ctx context.Context, i provider.Instance) error {
	ip, err := p.publicIP(ctx, i)
	if err != nil {
		return err
	}
	return waitForTCP(ctx, ip, 22, readyTimeout(i))
}

func waitForTCP(ctx context.Context, host string, port int, timeout time.Duration) error {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	deadline := time.Now().Add(timeout)
	delay := time.Second
	for {
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		c, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
		cancel()
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not reachable after %s: %w", timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 4*time.Second {
			delay += time.Second
		}
	}
}
