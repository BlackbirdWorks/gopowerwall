package commands_test

import (
	"testing"

	"github.com/blackbirdworks/gopowerwall/commands"
)

func TestCommandsTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		run       func() error
		name      string
		wantError bool
	}{
		{
			name: "version command",
			run: func() error {
				cmd := commands.VersionCmd{}

				return cmd.Run()
			},
			wantError: false,
		},
		{
			name: "setup command",
			run: func() error {
				cmd := commands.SetupCmd{}

				return cmd.Run()
			},
			wantError: false,
		},
		{
			name: "authtoken command",
			run: func() error {
				cmd := commands.AuthTokenCmd{}

				return cmd.Run()
			},
			wantError: false,
		},
		{
			name: "cloudcheck command",
			run: func() error {
				cmd := commands.CloudCheckCmd{}

				return cmd.Run()
			},
			wantError: false,
		},
		{
			name: "tedapi command",
			run: func() error {
				cmd := commands.TedapiCmd{Host: "127.0.0.1"}

				return cmd.Run()
			},
			wantError: false,
		},
		{
			name: "register command",
			run: func() error {
				cmd := commands.RegisterCmd{}

				return cmd.Run()
			},
			wantError: false,
		},
		{
			name: "set command with no actions fails",
			run: func() error {
				cmd := commands.SetCmd{Reserve: -1}

				return cmd.Run()
			},
			wantError: true,
		},
		{
			name: "get command fails without connection",
			run: func() error {
				//nolint:modernize // modernize false positive for embedded struct literals in Go
				cmd := commands.GetCmd{
					ConnectionFlags: commands.ConnectionFlags{
						Host:  "192.0.2.1",
						Local: true,
					},
				}

				return cmd.Run()
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.run()
			if (err != nil) != tt.wantError {
				t.Errorf("got error %v, wantError %v", err, tt.wantError)
			}
		})
	}
}
