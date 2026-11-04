package cli

import (
	"errors"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
)

func newChatHostCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "chat-host",
		Short:              "Run a persistent Chat provider host (internal)",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			protocol := persistenthost.ProtocolRaw
			fingerprint := ""
			separator := 3
			if len(args) > 3 && (args[3] == string(persistenthost.ProtocolACP) || args[3] == string(persistenthost.ProtocolUnreal)) {
				protocol = persistenthost.Protocol(args[3])
				if len(args) > 4 {
					fingerprint = strings.TrimSpace(args[4])
				}
				separator = 5
			}
			if len(args) < separator+2 || args[separator] != "--" || (protocol != persistenthost.ProtocolRaw && fingerprint == "") {
				return usageError{errors.New("chat-host requires <session> <data-dir> <workdir> [acp|unreal <fingerprint>] -- <provider> [args...]")}
			}
			ctx, stop := persistenthost.SignalContext(cmd.Context())
			defer stop()
			return persistenthost.Run(ctx, persistenthost.Config{
				SessionID:            strings.TrimSpace(args[0]),
				DataDir:              args[1],
				Workdir:              args[2],
				Env:                  os.Environ(),
				Argv:                 args[separator+1:],
				Protocol:             protocol,
				OwnershipFingerprint: fingerprint,
			})
		},
	}
}
