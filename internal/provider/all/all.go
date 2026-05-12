// Package all imports every concrete provider so they're registered with the
// default provider registry via side effects. Import this package once from
// the CLI to wire everything together.
package all

import (
	"github.com/edihasaj/vmlab/internal/provider"
	"github.com/edihasaj/vmlab/internal/provider/parallels"
)

func init() {
	provider.SideEffectRegister(parallels.New())
}
