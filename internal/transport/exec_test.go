package transport

import "testing"

func TestNeedsWindowsFileOutputForOpenSSH(t *testing.T) {
	t.Parallel()

	tests := []struct {
		goos string
		name string
		want bool
	}{
		{goos: "windows", name: "ssh", want: true},
		{goos: "windows", name: `C:\Windows\System32\OpenSSH\ssh.exe`, want: true},
		{goos: "windows", name: "scp.exe", want: true},
		{goos: "windows", name: "git.exe", want: false},
		{goos: "darwin", name: "ssh", want: false},
		{goos: "linux", name: "scp", want: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.goos+"/"+test.name, func(t *testing.T) {
			t.Parallel()
			if got := needsWindowsFileOutput(test.goos, test.name); got != test.want {
				t.Fatalf("needsWindowsFileOutput(%q, %q) = %v, want %v", test.goos, test.name, got, test.want)
			}
		})
	}
}
