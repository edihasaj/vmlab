package evidence

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
)

// JUnit XML shape — minimal compatibility with the Jenkins/CircleCI surface.
type junitTestsuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Name     string           `xml:"name,attr"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Time     float64          `xml:"time,attr"`
	Suites   []junitTestsuite `xml:"testsuite"`
}

type junitTestsuite struct {
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Time     float64         `xml:"time,attr"`
	Cases    []junitTestcase `xml:"testcase"`
}

type junitTestcase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      float64       `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
}

type junitFailure struct {
	Type    string `xml:"type,attr"`
	Message string `xml:"message,attr"`
}

// WriteJUnit emits a junit.xml summary under the run directory. Each target
// becomes a <testsuite>; the run itself is the parent <testsuites>. For shell
// runs without explicit steps, the suite holds a single case named "command".
func (r *Run) WriteJUnit() (string, error) {
	root := junitTestsuites{
		Name: "vmlab:" + r.meta.ID,
		Time: float64(r.meta.DurationMs) / 1000,
	}
	for _, ts := range r.meta.Targets {
		suite := junitTestsuite{
			Name: ts.Name,
			Time: float64(ts.Duration) / 1000,
		}
		steps, _ := readSteps(filepath.Join(r.Dir, "targets", sanitize(ts.Name), "steps.json"))
		if len(steps) == 0 {
			tc := junitTestcase{
				Name:      "command",
				Classname: ts.Transport + "." + ts.Name,
				Time:      float64(ts.Duration) / 1000,
			}
			if ts.ExitCode != 0 || ts.Error != "" {
				msg := ts.Error
				if msg == "" {
					msg = fmt.Sprintf("exit %d", ts.ExitCode)
				}
				tc.Failure = &junitFailure{Type: "vmlab.failure", Message: msg}
			}
			suite.Cases = append(suite.Cases, tc)
		} else {
			for _, s := range steps {
				tc := junitTestcase{
					Name:      fmt.Sprintf("step-%d-%s", s.Index, s.Kind),
					Classname: ts.Transport + "." + ts.Name,
					Time:      float64(s.DurationMs) / 1000,
				}
				if s.ExitCode != 0 || s.Error != "" {
					msg := s.Error
					if msg == "" {
						msg = fmt.Sprintf("exit %d", s.ExitCode)
					}
					tc.Failure = &junitFailure{Type: "vmlab.step", Message: msg}
				}
				suite.Cases = append(suite.Cases, tc)
			}
		}
		suite.Tests = len(suite.Cases)
		for _, c := range suite.Cases {
			if c.Failure != nil {
				suite.Failures++
			}
		}
		root.Tests += suite.Tests
		root.Failures += suite.Failures
		root.Suites = append(root.Suites, suite)
	}

	path := filepath.Join(r.Dir, "junit.xml")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(xml.Header); err != nil {
		return "", err
	}
	enc := xml.NewEncoder(f)
	enc.Indent("", "  ")
	if err := enc.Encode(root); err != nil {
		return "", err
	}
	return path, nil
}
