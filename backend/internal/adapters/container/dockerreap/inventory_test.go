package dockerreap

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestSessionContainersParsesLabels(t *testing.T) {
	fr := &fakeRunner{t: t, script: []fakeCall{{out: []byte("abc\tmer-1\t\tpg-mer-1\ndef\tmer-2\ttrue\tregistry\n\tbad\t\t\n")}}}
	got, err := newWithRunner(fr).SessionContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (ports.LabeledResource{ID: "abc", Name: "pg-mer-1", Session: "mer-1"}) || !got[1].Spare {
		t.Fatalf("containers = %+v", got)
	}
	if args := strings.Join(fr.calls[0], " "); !strings.Contains(args, "label="+SessionLabel) || !strings.Contains(args, "ps -a") {
		t.Fatalf("argv = %s", args)
	}
}

func TestSessionVolumesParsesLabels(t *testing.T) {
	fr := &fakeRunner{t: t, script: []fakeCall{{out: []byte("pgdata\tmer-1\tfalse\n")}}}
	got, err := newWithRunner(fr).SessionVolumes(context.Background())
	if err != nil || len(got) != 1 || got[0].ID != "pgdata" || got[0].Session != "mer-1" || got[0].Spare {
		t.Fatalf("volumes = %+v, %v", got, err)
	}
}

func TestInventoryWithoutDockerListsNothing(t *testing.T) {
	fr := &fakeRunner{t: t, script: []fakeCall{
		{err: &exec.Error{Name: "docker", Err: exec.ErrNotFound}},
		{err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")},
	}}
	r := newWithRunner(fr)
	if got, err := r.SessionContainers(context.Background()); err != nil || got != nil {
		t.Fatalf("containers without docker = %v, %v", got, err)
	}
	if got, err := r.SessionVolumes(context.Background()); err != nil || got != nil {
		t.Fatalf("volumes without docker = %v, %v", got, err)
	}
}
