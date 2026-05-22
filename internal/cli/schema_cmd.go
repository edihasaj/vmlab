// Schema command — dumps the JSON schemas embedded in the binary so agents
// can introspect what shape a target / flow / instance YAML must take without
// needing to find the source tree.
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/edihasaj/vmlab/internal/schema"
	"github.com/spf13/cobra"
)

func newSchemaCmd() *cobra.Command {
	var pretty bool
	c := &cobra.Command{
		Use:   "schema <kind>",
		Short: "Print the JSON schema for target | flow | instance YAML",
		Long: `Print the JSON schema vmlab uses to validate user YAML.

Kinds:
  target    schema for files in ~/.vmlab/targets/ and .vmlab/targets/
  flow      schema for files in flows/
  instance  schema for files in ~/.vmlab/instances/ and .vmlab/instances/

Default is pretty-printed; --raw passes the embedded bytes through unchanged.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw []byte
			switch args[0] {
			case "target", "targets":
				raw = schema.RawTarget()
			case "flow", "flows":
				raw = schema.RawFlow()
			case "instance", "instances":
				raw = schema.RawInstance()
			default:
				return fmt.Errorf("unknown schema kind: %q (want target | flow | instance)", args[0])
			}
			out := cmd.OutOrStdout()
			if !pretty {
				_, err := out.Write(raw)
				return err
			}
			var buf bytes.Buffer
			if err := json.Indent(&buf, raw, "", "  "); err != nil {
				return err
			}
			if _, err := out.Write(buf.Bytes()); err != nil {
				return err
			}
			_, err := out.Write([]byte("\n"))
			return err
		},
	}
	c.Flags().BoolVar(&pretty, "pretty", true, "pretty-print (default true; --pretty=false for raw)")
	return c
}
