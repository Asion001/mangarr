package moodscore

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Tokenizer is CLIP's byte-level BPE tokenizer, read from the model's
// tokenizer.json: lowercased, split into words, letters and punctuation,
// each word's bytes mapped to printable characters, merged pair by pair
// in the learned order, the last piece of a word marked with </w>.
type Tokenizer struct {
	vocab map[string]int64
	ranks map[[2]string]int
	bytes [256]string
	start int64
	end   int64
}

// maxTokens is the text model's context length.
const maxTokens = 77

var tokenRe = regexp.MustCompile(`<\|startoftext\|>|<\|endoftext\|>|'s|'t|'re|'ve|'m|'ll|'d|[\p{L}]+|[\p{N}]|[^\s\p{L}\p{N}]+`)

// LoadTokenizer reads a tokenizer.json.
func LoadTokenizer(r io.Reader) (*Tokenizer, error) {
	var doc struct {
		Model struct {
			Vocab  map[string]int64 `json:"vocab"`
			Merges json.RawMessage  `json:"merges"`
		} `json:"model"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("tokenizer: %w", err)
	}
	// merges are "a b" strings (older files) or ["a", "b"] pairs
	var pairs [][2]string
	var flat []string
	if err := json.Unmarshal(doc.Model.Merges, &flat); err == nil {
		for _, m := range flat {
			a, b, ok := strings.Cut(m, " ")
			if ok {
				pairs = append(pairs, [2]string{a, b})
			}
		}
	} else if err := json.Unmarshal(doc.Model.Merges, &pairs); err != nil {
		return nil, fmt.Errorf("tokenizer merges: %w", err)
	}
	t := &Tokenizer{vocab: doc.Model.Vocab, ranks: make(map[[2]string]int, len(pairs))}
	for i, p := range pairs {
		t.ranks[p] = i
	}
	var ok1, ok2 bool
	t.start, ok1 = t.vocab["<|startoftext|>"]
	t.end, ok2 = t.vocab["<|endoftext|>"]
	if !ok1 || !ok2 || len(t.vocab) == 0 {
		return nil, fmt.Errorf("tokenizer: not a CLIP tokenizer")
	}
	t.bytes = byteUnicode()
	return t, nil
}

// byteUnicode maps each byte to a printable character, as GPT-2 and CLIP
// do: printable bytes stand for themselves, the rest move above 255.
func byteUnicode() [256]string {
	var out [256]string
	n := 0
	for b := 0; b < 256; b++ {
		if (b >= '!' && b <= '~') || (b >= 0xA1 && b <= 0xAC) || (b >= 0xAE && b <= 0xFF) {
			out[b] = string(rune(b))
		} else {
			out[b] = string(rune(256 + n))
			n++
		}
	}
	return out
}

// Encode turns a text into token ids: start, the text's tokens, end
// (at most maxTokens).
func (t *Tokenizer) Encode(text string) []int64 {
	text = strings.ToLower(strings.Join(strings.Fields(norm.NFC.String(text)), " "))
	ids := []int64{t.start}
	for _, word := range tokenRe.FindAllString(text, -1) {
		var mapped []string
		for _, b := range []byte(word) {
			mapped = append(mapped, t.bytes[b])
		}
		for _, piece := range t.bpe(mapped) {
			if id, ok := t.vocab[piece]; ok {
				ids = append(ids, id)
			} else {
				ids = append(ids, t.end) // the unknown token is end of text
			}
		}
	}
	if len(ids) > maxTokens-1 {
		ids = ids[:maxTokens-1]
	}
	return append(ids, t.end)
}

// bpe merges a word's symbols by rank; the last carries </w>.
func (t *Tokenizer) bpe(symbols []string) []string {
	if len(symbols) == 0 {
		return nil
	}
	word := append([]string(nil), symbols...)
	word[len(word)-1] += "</w>"
	for len(word) > 1 {
		best, at := -1, -1
		for i := 0; i+1 < len(word); i++ {
			if r, ok := t.ranks[[2]string{word[i], word[i+1]}]; ok && (best < 0 || r < best) {
				best, at = r, i
			}
		}
		if at < 0 {
			break
		}
		a, b := word[at], word[at+1]
		var next []string
		for i := 0; i < len(word); i++ {
			if i+1 < len(word) && word[i] == a && word[i+1] == b {
				next = append(next, a+b)
				i++
			} else {
				next = append(next, word[i])
			}
		}
		word = next
	}
	return word
}
