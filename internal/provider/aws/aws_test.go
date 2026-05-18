package aws

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/edihasaj/vmlab/internal/provider"
)

func stubAws(t *testing.T, dir, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}
	argsFile := filepath.Join(dir, "aws.args")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
%s
`, argsFile, body)
	path := filepath.Join(dir, "aws")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return argsFile
}

func withPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func instance(name string, extra map[string]any) provider.Instance {
	if extra == nil {
		extra = map[string]any{}
	}
	return provider.Instance{
		Name:     name,
		Provider: "aws",
		Settings: map[string]any{"aws": extra},
	}
}

func TestStatusRunning(t *testing.T) {
	dir := t.TempDir()
	stubAws(t, dir, `cat <<JSON
[{"Id":"i-abc","State":"running","PublicIp":"3.4.5.6"}]
JSON
exit 0`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), instance("smoke", nil))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st != provider.StateRunning {
		t.Errorf("state=%v", st)
	}
}

func TestStatusStoppedMapsToStopped(t *testing.T) {
	dir := t.TempDir()
	stubAws(t, dir, `cat <<JSON
[{"Id":"i-abc","State":"stopped","PublicIp":""}]
JSON
exit 0`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), instance("smoke", nil))
	if err != nil || st != provider.StateStopped {
		t.Fatalf("status=%v err=%v", st, err)
	}
}

func TestStatusNoMatchIsNotFound(t *testing.T) {
	dir := t.TempDir()
	stubAws(t, dir, `echo "[]"; exit 0`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), instance("ghost", nil))
	if err != nil || st != provider.StateNotFound {
		t.Fatalf("status=%v err=%v", st, err)
	}
}

func TestStatusTerminatedIsNotFound(t *testing.T) {
	dir := t.TempDir()
	stubAws(t, dir, `cat <<JSON
[{"Id":"i-abc","State":"terminated","PublicIp":""}]
JSON
exit 0`)
	withPath(t, dir)

	st, err := New().Status(context.Background(), instance("zombie", nil))
	if err != nil || st != provider.StateNotFound {
		t.Fatalf("status=%v err=%v", st, err)
	}
}

func TestCreateArgsWired(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubAws(t, dir, `exit 0`)
	withPath(t, dir)

	err := New().create(context.Background(), instance("smoke", map[string]any{
		"imageId":          "ami-0123456789abcdef0",
		"instanceType":     "t4g.small",
		"keyName":          "edi-mac",
		"securityGroupIds": "sg-aaa,sg-bbb",
		"subnetId":         "subnet-xyz",
		"region":           "eu-west-1",
	}))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	for _, want := range []string{"--region eu-west-1", "ec2 run-instances",
		"--image-id ami-0123456789abcdef0", "--instance-type t4g.small",
		"--key-name edi-mac", "--security-group-ids sg-aaa sg-bbb",
		"--subnet-id subnet-xyz", "Key=vmlab,Value=smoke"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in args:\n%s", want, got)
		}
	}
}

func TestDownDestroyCallsTerminate(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubAws(t, dir, `case "$*" in
  *"describe-instances"*) cat <<JSON
[{"Id":"i-xyz","State":"running","PublicIp":"1.2.3.4"}]
JSON
  ;;
esac
exit 0`)
	withPath(t, dir)

	if err := New().Down(context.Background(), instance("smoke", nil), provider.DisposeDestroy); err != nil {
		t.Fatalf("down: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(got), "terminate-instances") {
		t.Fatalf("expected terminate-instances call:\n%s", got)
	}
}

func TestDownSuspendCallsStop(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubAws(t, dir, `case "$*" in
  *"describe-instances"*) cat <<JSON
[{"Id":"i-xyz","State":"running","PublicIp":"1.2.3.4"}]
JSON
  ;;
esac
exit 0`)
	withPath(t, dir)

	if err := New().Down(context.Background(), instance("smoke", nil), provider.DisposeSuspend); err != nil {
		t.Fatalf("down: %v", err)
	}
	got, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(got), "stop-instances") {
		t.Fatalf("expected stop-instances call:\n%s", got)
	}
}

func TestDoctorReportsCallerIdentity(t *testing.T) {
	dir := t.TempDir()
	stubAws(t, dir, `cat <<JSON
{"UserId":"AID","Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/edi"}
JSON
exit 0`)
	withPath(t, dir)

	h := New().Doctor(context.Background(), instance("smoke", nil))
	if !h.OK || !strings.Contains(h.Message, "123456789012") {
		t.Fatalf("doctor=%+v", h)
	}
}

func TestRegionAndProfilePrepended(t *testing.T) {
	dir := t.TempDir()
	argsFile := stubAws(t, dir, `exit 0`)
	withPath(t, dir)

	_, _ = New().run(context.Background(),
		instance("smoke", map[string]any{"region": "us-east-1", "profile": "dev"}),
		"ec2", "describe-instances")
	got, _ := os.ReadFile(argsFile)
	line := strings.TrimSpace(string(got))
	if !strings.HasPrefix(line, "--region us-east-1 --profile dev") {
		t.Fatalf("region/profile not prepended: %q", line)
	}
}
