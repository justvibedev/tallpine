package browserruntime

import (
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// helloRejection reports why a runtime's first frame is not an acceptable hello,
// or nil if it is.
func helloRejection(frame []byte, token string) *CommandError {
	var hello wireMessage
	switch {
	case json.Unmarshal(frame, &hello) != nil || hello.Type != "hello":
		return &CommandError{Code: "BROWSER_RUNTIME_BAD_HELLO", Message: "first frame was not a hello"}
	case hello.Version < minSupportedProtocolVersion || hello.Version > ProtocolVersion:
		return &CommandError{
			Code:    "BROWSER_RUNTIME_PROTOCOL_MISMATCH",
			Message: fmt.Sprintf("daemon speaks browser runtime protocol %d-%d, runtime sent %d", minSupportedProtocolVersion, ProtocolVersion, hello.Version),
		}
	case !validRuntimeToken(token, hello.Token):
		return &CommandError{
			Code:    "BROWSER_RUNTIME_TOKEN_REJECTED",
			Message: "browser runtime token does not match the one this daemon was started with",
		}
	}
	return nil
}

func writeHelloReply(conn net.Conn, msg wireMessage) error {
	frame, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(helloReplyTimeout)); err != nil {
		return err
	}
	defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
	_, err = conn.Write(append(frame, '\n'))
	return err
}

func validRuntimeToken(expected, actual string) bool {
	if expected == "" {
		return actual == ""
	}
	return hmac.Equal([]byte(expected), []byte(actual))
}
