package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/artifactstorage"
)

type fakeArtifactStorage struct {
	reportDays int
	action     artifactstorage.Action
	ids        []domain.SessionID
	retained   bool
}

func (f *fakeArtifactStorage) Report(_ context.Context, days int) (artifactstorage.Report, error) {
	f.reportDays = days
	if days < 0 {
		return artifactstorage.Report{}, artifactstorage.ErrInvalid
	}
	return artifactstorage.Report{
		OlderThanDays: 30, WarnBytes: 100, TotalBytes: 250, VersionsBytes: 50, EligibleBytes: 200,
		Entries: []artifactstorage.Entry{{SessionID: "s1", ProjectID: "p", SizeBytes: 200, OverWarn: true, Eligible: true, UpdatedAt: time.Unix(0, 0)}},
	}, nil
}

func (f *fakeArtifactStorage) Retain(_ context.Context, action artifactstorage.Action, _ int, ids []domain.SessionID) ([]artifactstorage.Result, error) {
	f.retained, f.action, f.ids = true, action, ids
	return []artifactstorage.Result{{SessionID: ids[0], Status: artifactstorage.StatusArchived, FreedBytes: 200, ArchivePath: "/x.tar.gz"}}, nil
}

func artifactStorageRouter(svc ArtifactStorageService) http.Handler {
	r := chi.NewRouter()
	(&ArtifactStorageController{Svc: svc}).Register(r)
	return r
}

func TestArtifactStorage_Report(t *testing.T) {
	svc := &fakeArtifactStorage{}
	rec := httptest.NewRecorder()
	artifactStorageRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/storage?olderThanDays=14", nil))
	if rec.Code != http.StatusOK || svc.reportDays != 14 {
		t.Fatalf("code %d days %d body %s", rec.Code, svc.reportDays, rec.Body)
	}
	var out ArtifactStorageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.TotalBytes != 250 || len(out.Sessions) != 1 || !out.Sessions[0].OverWarn || !out.Sessions[0].Eligible {
		t.Fatalf("out = %+v", out)
	}
}

func TestArtifactStorage_RetainRequiresConfirm(t *testing.T) {
	svc := &fakeArtifactStorage{}
	body := `{"action":"delete","olderThanDays":30,"sessionIds":["s1"]}`
	rec := httptest.NewRecorder()
	artifactStorageRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/artifacts/storage/retention", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest || svc.retained || !strings.Contains(rec.Body.String(), "ARTIFACT_RETENTION_UNCONFIRMED") {
		t.Fatalf("code %d retained %v body %s", rec.Code, svc.retained, rec.Body)
	}
}

func TestArtifactStorage_RetainConfirmed(t *testing.T) {
	svc := &fakeArtifactStorage{}
	body := `{"action":"archive","olderThanDays":30,"sessionIds":["s1"],"confirm":true}`
	rec := httptest.NewRecorder()
	artifactStorageRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/artifacts/storage/retention", strings.NewReader(body)))
	if rec.Code != http.StatusOK || svc.action != artifactstorage.ActionArchive || len(svc.ids) != 1 {
		t.Fatalf("code %d body %s", rec.Code, rec.Body)
	}
	var out ArtifactRetentionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.FreedBytes != 200 || out.Results[0].Status != "archived" {
		t.Fatalf("out = %+v err %v", out, err)
	}
}

func TestArtifactStorage_InvalidWindowIs400(t *testing.T) {
	rec := httptest.NewRecorder()
	artifactStorageRouter(&fakeArtifactStorage{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/storage?olderThanDays=-3", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code %d body %s", rec.Code, rec.Body)
	}
}

func TestArtifactStorage_NilServiceIs501(t *testing.T) {
	rec := httptest.NewRecorder()
	artifactStorageRouter(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/storage", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("code %d", rec.Code)
	}
}
