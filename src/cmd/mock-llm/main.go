// mock-llm is the controllable LLM stand-in that BDD scenarios point the CLI
// at via ACCURATE_REVIEWER_MOCK_URL. The server has two surfaces:
//   - /complete         the CLI's worker calls land here, replies come from the script
//   - /script           the BDD harness POSTs the scripted responses here
//   - /prompts          the BDD harness GETs every prompt the server has seen
//   - /reset            clear script and prompt log between scenarios
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"sync"
)

type scriptEntry struct {
	Worker  string `json:"worker"`
	Text    string `json:"text"`
	Error   string `json:"error"`
	Tokens  int    `json:"tokens"`
	DelayMs int    `json:"delay_ms"`
}

type promptEntry struct {
	Role   string `json:"role"`
	Worker string `json:"worker"`
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type server struct {
	mu      sync.Mutex
	script  map[string]scriptEntry
	prompts []promptEntry
}

func (s *server) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.script = map[string]scriptEntry{}
	s.prompts = nil
}

func (s *server) handleScript(w http.ResponseWriter, r *http.Request) {
	var entries []scriptEntry
	if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.script == nil {
		s.script = map[string]scriptEntry{}
	}
	for _, e := range entries {
		s.script[e.Worker] = e
	}
	w.WriteHeader(204)
}

func (s *server) handleComplete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role, Worker, Model, Prompt string
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.mu.Lock()
	s.prompts = append(s.prompts, promptEntry{
		Role: req.Role, Worker: req.Worker, Model: req.Model, Prompt: req.Prompt,
	})
	entry, ok := s.script[req.Worker]
	s.mu.Unlock()

	resp := map[string]any{}
	if !ok {
		// Default: empty findings array, 0 tokens.
		resp["text"] = "[]"
	} else {
		resp["error"] = entry.Error
		resp["text"] = entry.Text
		resp["used_tokens"] = entry.Tokens
		resp["delay_ms"] = entry.DelayMs
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *server) handlePrompts(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.prompts
	if out == nil {
		out = []promptEntry{}
	}
	_ = json.NewEncoder(w).Encode(out)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8765", "listen address")
	flag.Parse()
	s := &server{script: map[string]scriptEntry{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/complete", s.handleComplete)
	mux.HandleFunc("/script", s.handleScript)
	mux.HandleFunc("/prompts", s.handlePrompts)
	mux.HandleFunc("/reset", func(w http.ResponseWriter, _ *http.Request) {
		s.reset()
		w.WriteHeader(204)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	})

	log.SetOutput(os.Stderr)
	log.Printf("mock-llm listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
