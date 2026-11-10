package copycheck

import "hash/fnv"

// Fingerprint is one selected k-gram: its hash and where it starts in the
// sequence it came from.
type Fingerprint struct {
	Hash uint64
	Pos  int
}

func hashStrings(s []string) uint64 {
	h := fnv.New64a()
	for _, x := range s {
		h.Write([]byte(x))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// Winnow selects fingerprints from seq: the hash of every k-gram is computed,
// and from every window of w consecutive hashes the smallest is kept (the
// rightmost on a tie), once.
//
// The guarantee that makes it a detector and not a sampler: any substring
// shared by two sequences and at least k+w-1 long yields at least one common
// fingerprint, whatever surrounds it. skip, when not nil, drops the k-grams it
// returns true for BEFORE selection (a table of zeros says nothing).
func Winnow(seq []string, k, w int, skip func(gram []string) bool) []Fingerprint {
	if k < 1 || w < 1 || len(seq) < k {
		return nil
	}
	n := len(seq) - k + 1
	const dropped = ^uint64(0)
	hs := make([]uint64, n)
	for i := range hs {
		if skip != nil && skip(seq[i:i+k]) {
			hs[i] = dropped
			continue
		}
		hs[i] = hashStrings(seq[i : i+k])
		if hs[i] == dropped {
			hs[i]--
		}
	}
	var out []Fingerprint
	last := -1
	win := w
	if n < w {
		win = n
	}
	for s := 0; s+win <= n; s++ {
		best := -1
		for j := s; j < s+win; j++ {
			if hs[j] == dropped {
				continue
			}
			if best < 0 || hs[j] <= hs[best] {
				best = j
			}
		}
		if best >= 0 && best != last {
			out = append(out, Fingerprint{Hash: hs[best], Pos: best})
			last = best
		}
	}
	return out
}
