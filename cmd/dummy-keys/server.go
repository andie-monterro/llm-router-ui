package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/michaelquigley/df/dl"
)

// faultNone and friends name the failure modes the server can inject. the
// interesting behaviour in the gateway's key-source design is failure
// behaviour — last-known-good, staleness, exclusion, the unsolicited-304
// guard — and none of it is observable against a server that always behaves.
const (
	faultNone         = "none"
	faultStatus       = "status"       // reply with --fault-status instead of the key set
	faultCount        = "count"        // send a count that disagrees with keys
	faultPagination   = "pagination"   // announce pagination via a Link header
	faultUnsolicited  = "unsolicited"  // reply 304 whether or not the request was conditional
	faultStall        = "stall"        // hold the response open past any sane timeout
	faultMalformed    = "malformed"    // send a body that is not the v1 envelope
	faultUnknownField = "unknownfield" // send an extra record field, which strict decoding rejects
)

// faults lists every accepted --fault value, for the flag's help text and for
// validation at startup.
var faults = []string{
	faultNone, faultStatus, faultCount, faultPagination,
	faultUnsolicited, faultStall, faultMalformed, faultUnknownField,
}

type config struct {
	path        string        // key file served as the response body
	token       string        // when set, required as Authorization: Bearer
	fault       string        // failure mode to inject
	faultStatus int           // status returned by the "status" fault
	stallFor    time.Duration // how long the "stall" fault holds the response
}

// server serves the published GET /v1/keys contract from a file. the file is
// re-read per request, so editing it and watching the gateway converge is the
// demo. it deliberately does not import the gateway's unexported wire types:
// the contract is the bytes, and a management plane implements those.
type server struct {
	cfg config

	mu   sync.Mutex
	etag string // etag of the last body served, recomputed per read
}

func newServer(cfg config) *server {
	if cfg.fault == "" {
		cfg.fault = faultNone
	}
	if cfg.faultStatus == 0 {
		cfg.faultStatus = http.StatusInternalServerError
	}
	if cfg.stallFor == 0 {
		cfg.stallFor = time.Minute
	}
	return &server{cfg: cfg}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/keys", s.handleKeys)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	mux.HandleFunc("/v1/keys", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})
	return mux
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *server) handleKeys(w http.ResponseWriter, r *http.Request) {
	if s.cfg.token != "" {
		if r.Header.Get("Authorization") != "Bearer "+s.cfg.token {
			dl.Warnf("rejecting request with a missing or wrong bearer token")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	switch s.cfg.fault {
	case faultStall:
		dl.Infof("stalling the response for %s", s.cfg.stallFor)
		select {
		case <-time.After(s.cfg.stallFor):
		case <-r.Context().Done():
		}
		return
	case faultStatus:
		dl.Infof("injecting HTTP %d", s.cfg.faultStatus)
		http.Error(w, "injected fault", s.cfg.faultStatus)
		return
	case faultUnsolicited:
		// answer 304 regardless of If-None-Match. a v1 gateway must refuse
		// this: with no validator in the request there is nothing to confirm.
		dl.Infof("injecting an unsolicited 304 (conditional=%v)", r.Header.Get("If-None-Match") != "")
		w.WriteHeader(http.StatusNotModified)
		return
	case faultMalformed:
		dl.Info("injecting a malformed body")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":1,"count":0}`)) // no keys member
		return
	}

	records, err := s.readRecords()
	if err != nil {
		dl.Errorf("read key file: %v", err)
		http.Error(w, "key file unavailable", http.StatusInternalServerError)
		return
	}

	if s.cfg.fault == faultUnknownField {
		dl.Info("injecting an unknown record field")
		for i := range records {
			records[i]["allowed_model"] = []string{"gpt-*"}
		}
	}

	count := len(records)
	if s.cfg.fault == faultCount {
		count = len(records) + 1
		dl.Infof("injecting a count mismatch: reporting %d for %d records", count, len(records))
	}

	body, err := json.Marshal(map[string]any{
		"version": 1,
		"count":   count,
		"keys":    records,
	})
	if err != nil {
		dl.Errorf("encode key set: %v", err)
		http.Error(w, "encode failure", http.StatusInternalServerError)
		return
	}

	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	s.mu.Lock()
	s.etag = etag
	s.mu.Unlock()

	if s.cfg.fault == faultPagination {
		dl.Info("injecting a pagination Link header")
		w.Header().Set("Link", `<https://example.invalid/v1/keys?page=2>; rel="next"`)
	}
	w.Header().Set("ETag", etag)

	// a conditional request whose validator still matches costs a round trip
	// instead of a payload, which is what makes polling cheap enough to be the
	// gateway's primary convergence mechanism.
	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
		dl.Infof("304 not modified (%d records)", len(records))
		w.WriteHeader(http.StatusNotModified)
		return
	}

	dl.Infof("200 serving %d records (count=%d, etag=%s)", len(records), count, etag)
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// readRecords loads the key file as a list of raw record maps. the file may be
// either a bare list or the full envelope, so an operator can point this at the
// same document the gateway's file source reads.
func (s *server) readRecords() ([]map[string]any, error) {
	data, err := os.ReadFile(s.cfg.path)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.Keys != nil {
		return envelope.Keys, nil
	}

	var bare []map[string]any
	if err := json.Unmarshal(data, &bare); err == nil {
		return bare, nil
	}
	return nil, fmt.Errorf("%s is neither a v1 envelope nor a bare record list", s.cfg.path)
}

// etagMatches implements the comparison a conditional request needs: a
// comma-separated list, `*`, and the weak prefix a cache may add.
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == etag {
			return true
		}
		if strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
