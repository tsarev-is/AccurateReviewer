package master

import "github.com/scaratec/accurate-reviewer/internal/llm"

// ProviderSet resolves the (provider, model) pair for one worker call.
// It is the multi-provider point of policy: the master never inspects
// the config directly to pick a provider — it asks the set.
//
// Resolution order, given a worker name and a fallback-engaged flag:
//
//  1. If fallback is engaged AND a Fallback provider was registered,
//     return (Fallback, FallbackModel). All workers fan out to the same
//     cheap path once the budget has tripped — the per-worker overrides
//     are intentionally abandoned, because mid-run heterogeneity makes
//     it hard to reason about what the report represents.
//  2. Otherwise, if a per-worker override exists, return that.
//  3. Otherwise, return the default (Default, DefaultModel).
//
// The set is built once in cli/review.go after config parse; the master
// keeps a pointer to it for the duration of one Review call.
type ProviderSet struct {
	Default       llm.Provider
	DefaultModel  string
	ByWorker      map[string]workerEntry
	Fallback      llm.Provider
	FallbackModel string
}

type workerEntry struct {
	Provider llm.Provider
	Model    string
}

// For returns the (provider, model) the master should spawn for the
// given worker. fallbackEngaged is sticky once true — see Master.Review
// for how the flag flips on budget threshold and never unflips.
func (s *ProviderSet) For(workerName string, fallbackEngaged bool) (llm.Provider, string) {
	if fallbackEngaged && s.Fallback != nil {
		return s.Fallback, s.FallbackModel
	}
	if entry, ok := s.ByWorker[workerName]; ok {
		provider := entry.Provider
		if provider == nil {
			provider = s.Default
		}
		model := entry.Model
		if model == "" {
			model = s.DefaultModel
		}
		return provider, model
	}
	return s.Default, s.DefaultModel
}

// RegisterWorker adds a per-worker override. Either of provider/model
// may be empty — empty values fall back to (Default, DefaultModel) at
// resolution time. Builders call this from cli/review.go once per
// configured override.
func (s *ProviderSet) RegisterWorker(workerName string, provider llm.Provider, model string) {
	if s.ByWorker == nil {
		s.ByWorker = map[string]workerEntry{}
	}
	s.ByWorker[workerName] = workerEntry{Provider: provider, Model: model}
}
