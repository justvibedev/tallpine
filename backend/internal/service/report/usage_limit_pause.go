package report

import (
	"fmt"
	"time"
)

// UsageLimitRecheck caps how long a paused batch is parked before the pause is
// checked again.
const UsageLimitRecheck = 3 * time.Minute

// UsageLimitPausedError is returned by a delivery target whose provider account
// is out of quota until Until. Delivering now would only start a turn that
// fails, so the coordinator parks the batch until then.
type UsageLimitPausedError struct{ Until time.Time }

func (e *UsageLimitPausedError) Error() string {
	return fmt.Sprintf("session paused by provider usage limit until %s", e.Until.UTC().Format(time.RFC3339))
}
