package daemon

import (
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// The order services are wired in is the order Run always built them; later
// stages read what earlier ones stored on boot.
func TestBootStagesRunInTheOriginalOrder(t *testing.T) {
	var got []string
	for _, stage := range (&boot{}).bootStages() {
		name := runtime.FuncForPC(reflect.ValueOf(stage).Pointer()).Name()
		got = append(got, strings.TrimSuffix(name[strings.LastIndex(name, ".")+1:], "-fm"))
	}
	want := []string{"wireStorage", "wireChat", "wireAgents", "wireSessions", "wireReview", "wireMobile", "wireHTTP", "serve"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("boot stages = %v, want %v", got, want)
	}
}

// Run's deferred teardown ran last-registered-first; cleanups keep that order.
func TestBootCleanupsRunLastRegisteredFirst(t *testing.T) {
	b := &boot{}
	var order []int
	for i := 1; i <= 4; i++ {
		b.cleanup(func() { order = append(order, i) })
	}
	b.runCleanups()
	if !reflect.DeepEqual(order, []int{4, 3, 2, 1}) {
		t.Fatalf("cleanup order = %v, want [4 3 2 1]", order)
	}
}
