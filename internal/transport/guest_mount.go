package transport

import "github.com/edihasaj/vmlab/internal/target"

// GuestMounter is an optional Transport extension. Sync establishes a
// host→guest pipeline; GuestMount answers "where inside the guest does that
// source now live?" so the flow runner can expose $VMLAB_SYNC_DIR to
// subsequent steps. Returns "" when the transport can't predict a stable
// guest path (or never landed bits inside the guest in the first place).
type GuestMounter interface {
	GuestMount(t target.Target, src string) string
}

// GuestMountFor returns the guest-side mount path for src, or "" if the
// transport doesn't implement GuestMounter. Centralised so callers don't
// repeat the type-assert boilerplate.
func GuestMountFor(tr Transport, t target.Target, src string) string {
	if gm, ok := tr.(GuestMounter); ok {
		return gm.GuestMount(t, src)
	}
	return ""
}
