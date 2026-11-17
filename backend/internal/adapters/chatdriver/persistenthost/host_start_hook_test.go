package persistenthost

import (
	"os"
	"time"
)

// init runs before TestMain, so a re-exec'd test binary acting as a chat host
// can simulate a host that launches slowly or dies before publishing.
func init() {
	if len(os.Args) < 2 || os.Args[1] != "chat-host" {
		return
	}
	if os.Getenv("TALLPINE_CHAT_HOST_TEST_EXIT") != "" {
		os.Exit(3)
	}
	if d, err := time.ParseDuration(os.Getenv("TALLPINE_CHAT_HOST_TEST_DELAY")); err == nil {
		time.Sleep(d)
	}
}
