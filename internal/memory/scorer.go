package memory

import (
	"context"
	"regexp"
	"strings"

	"traceiq/internal/model"
)

var wordRe = regexp.MustCompile(`[a-z0-9]+`)

// tokenize is a small, dependency-free lexical tokenizer: lowercase,
// alphanumeric runs.
func tokenize(s string) []string {
	return wordRe.FindAllString(strings.ToLower(s), -1)
}

func tokenSet(tokens ...[]string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, ts := range tokens {
		for _, t := range ts {
			if t != "" {
				set[t] = struct{}{}
			}
		}
	}
	return set
}

func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// lexicalScorer is memory's default, offline SimilarityScorer (DR-19 §19.3:
// "the whole system works fully offline with no embedding-model dependency
// by default"). It scores a query's fingerprint tokens against each
// candidate's FingerprintTerms, and (only when the query supplies free
// text) blends in a secondary Jaccard component over free text against the
// candidate's Symptom/RootCause/BodyMarkdown prose.
//
// Bug fixed this pass: an earlier version pooled fingerprint tokens and
// free-text tokens into one combined bag per side and Jaccarded the union.
// Since Similar()'s normal call (FR-F08-2) supplies only a Fingerprint and
// leaves Query.Text empty, that combined-bag Jaccard was diluted by every
// candidate's Symptom/RootCause/BodyMarkdown words — which have zero
// overlap with a token-only query — pushing even an *exact* fingerprint
// match below min_similarity (0.35) for any candidate carrying nonempty
// prose (e.g. 3 shared fingerprint tokens plus 5 candidate-only prose
// tokens washes a perfect fingerprint match down to 3/8 = 0.375, and it
// gets worse as prose grows — a 3-token match against a candidate with
// just two one-word Symptom/RootCause fields already lands at 1/3 = 0.33,
// under threshold). That made Store.Similar's core, spec-mandated path
// (AC-F08-2, FR-F08-2) return nothing for what should be the store's best
// match. Fingerprint-token overlap is now scored on its own so a query
// that supplies no free text is judged purely on fingerprint similarity,
// matching DR-19 §19.2's framing of Similar() as fingerprint-driven with
// free text living in Search()/FTS instead.
//
// A full TF-IDF cosine component (mentioned alongside Jaccard in the task
// brief) is not implemented this pass — Jaccard alone satisfies "offline
// lexical, no embedding API" and the near-match acceptance test; see
// docs/reports/w11-correlate-memory.md "remaining".
type lexicalScorer struct{}

// NewLexicalScorer constructs memory's default offline SimilarityScorer.
func NewLexicalScorer() SimilarityScorer { return lexicalScorer{} }

func (lexicalScorer) Name() string { return "lexical" }

// textWeight is this scorer's judgment-call blend factor when a query
// supplies free text alongside a fingerprint: the fingerprint-token
// Jaccard still dominates (DR-19 §19.2 treats fingerprint tokens as the
// primary similarity signal), with the free-text Jaccard contributing a
// smaller secondary component.
const textWeight = 0.3

func (lexicalScorer) Score(ctx context.Context, tid model.TenantID, q Query, cand []Candidate) ([]Scored, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	qFPSet := tokenSet(q.Fingerprint.Tokens)
	qTextSet := tokenSet(tokenize(q.Text))
	out := make([]Scored, 0, len(cand))
	for _, c := range cand {
		r := c.Record
		fpScore := jaccard(qFPSet, tokenSet(r.FingerprintTerms))
		score := fpScore
		if len(qTextSet) > 0 {
			textScore := jaccard(qTextSet, tokenSet(tokenize(r.Symptom), tokenize(r.RootCause), tokenize(r.BodyMarkdown)))
			score = (1-textWeight)*fpScore + textWeight*textScore
		}
		out = append(out, Scored{Record: r, Score: score})
	}
	return out, nil
}
