package flow

import (
	"strings"

	"github.com/edihasaj/vmlab/internal/target"
)

// runtime holds the per-target mutable state a Flow Run accumulates: built-in
// variables ($VMLAB_SYNC_DIR set by the most recent successful sync step,
// $VMLAB_OS / $VMLAB_ARCH / $VMLAB_TARGET, etc). Kept in one place so the
// substitution and shell-wrapping helpers don't grow a long arg list.
type runtime struct {
	vars map[string]string
}

func newRuntime(t target.Target) *runtime {
	return &runtime{vars: map[string]string{
		"VMLAB_OS":     t.OSKind(),
		"VMLAB_ARCH":   t.SettingString("arch"),
		"VMLAB_TARGET": t.Name,
	}}
}

func (r *runtime) set(k, v string) { r.vars[k] = v }
func (r *runtime) get(k string) string {
	if r == nil {
		return ""
	}
	return r.vars[k]
}

// substitute replaces $VAR, ${VAR}, and %VAR% references with the runtime's
// current value. Unknown names expand to "" so a forgotten sync step shows
// up as a path-like "" instead of leaking the literal $VMLAB_SYNC_DIR into
// a shell command (where it would silently glob to nothing on POSIX and
// expand to the actual environment on Windows).
func (r *runtime) substitute(s string) string {
	if s == "" || r == nil {
		return s
	}
	return expandVars(s, r.vars)
}

// expandVars handles $NAME, ${NAME}, and %NAME% in one pass. Tight: enough
// for our built-ins. Not a full shell-quote-aware parser.
func expandVars(s string, vars map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch c {
		case '$':
			if i+1 < len(s) && s[i+1] == '{' {
				end := strings.IndexByte(s[i+2:], '}')
				if end >= 0 {
					name := s[i+2 : i+2+end]
					b.WriteString(vars[name])
					i += 2 + end + 1
					continue
				}
			}
			j := i + 1
			for j < len(s) && (isIdentByte(s[j])) {
				j++
			}
			if j > i+1 {
				name := s[i+1 : j]
				b.WriteString(vars[name])
				i = j
				continue
			}
			b.WriteByte(c)
			i++
		case '%':
			end := strings.IndexByte(s[i+1:], '%')
			if end > 0 {
				name := s[i+1 : i+1+end]
				if v, ok := vars[name]; ok {
					b.WriteString(v)
					i += 1 + end + 1
					continue
				}
			}
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func isIdentByte(c byte) bool {
	return c == '_' ||
		(c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9')
}

// wrapForExec composes the final shell line for a `run`/`assert`/`install`
// step on the target's OS, weaving in env exports and workdir change. The
// result is a single command line ready for transport.WrapShell.
//
// Windows note: cmd.exe can't `cd` into a UNC path (\\Mac\share) — pushd
// works there and quietly assigns a temp drive letter, so we always use
// pushd for windows targets.
func wrapForExec(t target.Target, cmd, workdir string, env map[string]string) string {
	if workdir == "" && len(env) == 0 {
		return cmd
	}
	if t.OSKind() == "windows" {
		var b strings.Builder
		for k, v := range env {
			b.WriteString(`set "`)
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(v)
			b.WriteString(`" && `)
		}
		if workdir != "" {
			// Always pushd: works for both regular paths and UNC mounts.
			b.WriteString(`pushd "`)
			b.WriteString(workdir)
			b.WriteString(`" && `)
		}
		b.WriteString(cmd)
		return b.String()
	}
	// POSIX: env K=V K2=V2 sh -c '...'  approximated as inline exports
	// since we hand the joined string to sh -lc anyway.
	var b strings.Builder
	for k, v := range env {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(shellSingleQuote(v))
		b.WriteByte(' ')
	}
	if len(env) > 0 {
		// inline VAR=val assignments don't export to subprocesses; use
		// `export` so children see them.
		b.Reset()
		for k, v := range env {
			b.WriteString("export ")
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(shellSingleQuote(v))
			b.WriteString(" && ")
		}
	}
	if workdir != "" {
		b.WriteString("cd ")
		b.WriteString(shellSingleQuote(workdir))
		b.WriteString(" && ")
	}
	b.WriteString(cmd)
	return b.String()
}

// mergedEnv layers step env over flow env, substituting variables on values.
// Returns nil when both sides are empty so callers can skip the prefix.
func mergedEnv(flowEnv, stepEnv map[string]string, rt *runtime) map[string]string {
	if len(flowEnv) == 0 && len(stepEnv) == 0 {
		return nil
	}
	out := make(map[string]string, len(flowEnv)+len(stepEnv))
	for k, v := range flowEnv {
		out[k] = rt.substitute(v)
	}
	for k, v := range stepEnv {
		out[k] = rt.substitute(v)
	}
	return out
}

func shellSingleQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
