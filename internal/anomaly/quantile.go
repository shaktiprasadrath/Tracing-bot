package anomaly

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"sort"
)

// p2Quantiles is the fixed, closed set of quantiles every P2Estimator tracks
// in parallel (DR-14 §14.1: "three tracked quantiles (0.50/0.95/0.99)").
var p2Quantiles = [3]float64{0.50, 0.95, 0.99}

// NewP2Estimator returns a zero-value-ready P2Estimator. The zero value
// itself is also usable directly (no allocation until the 6th Add call per
// quantile, when the init buffer is discarded).
func NewP2Estimator() *P2Estimator { return &P2Estimator{} }

var _ QuantileEstimator = (*P2Estimator)(nil)

// Add feeds one observation into all three tracked quantile markers, per
// the classic P² (piecewise-parabolic) algorithm.
func (e *P2Estimator) Add(v float64) {
	for qi, p := range p2Quantiles {
		e.addOne(qi, p, v)
	}
}

func (e *P2Estimator) addOne(qi int, p float64, v float64) {
	if e.count[qi] < 5 {
		e.init[qi] = append(e.init[qi], v)
		e.count[qi]++
		if e.count[qi] == 5 {
			buf := append([]float64(nil), e.init[qi]...)
			sort.Float64s(buf)
			for i := 0; i < 5; i++ {
				e.Markers[qi][i].Position = float64(i + 1)
				e.Markers[qi][i].Height = buf[i]
			}
			e.np[qi] = [5]float64{1, 1 + 2*p, 1 + 4*p, 3 + 2*p, 5}
		}
		return
	}

	m := &e.Markers[qi]
	dn := [5]float64{0, p / 2, p, (1 + p) / 2, 1}

	var k int
	switch {
	case v < m[0].Height:
		m[0].Height = v
		k = 0
	case v >= m[4].Height:
		m[4].Height = v
		k = 3
	default:
		for i := 0; i < 4; i++ {
			if v < m[i+1].Height {
				k = i
				break
			}
		}
	}
	for i := k + 1; i < 5; i++ {
		m[i].Position++
	}
	for i := 0; i < 5; i++ {
		e.np[qi][i] += dn[i]
	}
	for i := 1; i < 4; i++ {
		d := e.np[qi][i] - m[i].Position
		if (d >= 1 && m[i+1].Position-m[i].Position > 1) || (d <= -1 && m[i-1].Position-m[i].Position < -1) {
			sign := 1.0
			if d < 0 {
				sign = -1.0
			}
			qp := p2Parabolic(m, i, sign)
			if m[i-1].Height < qp && qp < m[i+1].Height {
				m[i].Height = qp
			} else {
				m[i].Height = p2Linear(m, i, sign)
			}
			m[i].Position += sign
		}
	}
	e.count[qi]++
}

func p2Parabolic(m *[5]struct{ Position, Height float64 }, i int, sign float64) float64 {
	np1, n, nm1 := m[i+1].Position, m[i].Position, m[i-1].Position
	qp1, q, qm1 := m[i+1].Height, m[i].Height, m[i-1].Height
	return q + sign/(np1-nm1)*((n-nm1+sign)*(qp1-q)/(np1-n)+
		(np1-n-sign)*(q-qm1)/(n-nm1))
}

func p2Linear(m *[5]struct{ Position, Height float64 }, i int, sign float64) float64 {
	j := i + int(sign)
	return m[i].Height + sign*(m[j].Height-m[i].Height)/(m[j].Position-m[i].Position)
}

// nearestQuantileIndex maps an arbitrary q onto the closest tracked slot.
func nearestQuantileIndex(q float64) int {
	best, bestD := 0, 1.0
	for i, p := range p2Quantiles {
		d := p - q
		if d < 0 {
			d = -d
		}
		if d < bestD {
			bestD = d
			best = i
		}
	}
	return best
}

// Quantile returns the tracked estimate nearest to q among {0.50,0.95,0.99}.
func (e *P2Estimator) Quantile(q float64) float64 {
	qi := nearestQuantileIndex(q)
	if e.count[qi] < 5 {
		if e.count[qi] == 0 {
			return 0
		}
		buf := append([]float64(nil), e.init[qi]...)
		sort.Float64s(buf)
		idx := int(q * float64(len(buf)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(buf) {
			idx = len(buf) - 1
		}
		return buf[idx]
	}
	return e.Markers[qi][2].Height
}

// Merge is a best-effort approximate merge: sample-count-weighted average of
// tracked heights. Exact P² merge has no closed form (the type is marked
// under-specified in anomaly.go); this keeps the estimator usable across a
// checkpoint/warm-start boundary without claiming exactness.
func (e *P2Estimator) Merge(other QuantileEstimator) error {
	o, ok := other.(*P2Estimator)
	if !ok {
		return fmt.Errorf("anomaly: P2Estimator.Merge: incompatible type %T", other)
	}
	for qi := range p2Quantiles {
		if o.count[qi] == 0 {
			continue
		}
		if e.count[qi] == 0 {
			e.Markers[qi] = o.Markers[qi]
			e.np[qi] = o.np[qi]
			e.count[qi] = o.count[qi]
			e.init[qi] = append([]float64(nil), o.init[qi]...)
			continue
		}
		total := float64(e.count[qi] + o.count[qi])
		for i := 0; i < 5; i++ {
			e.Markers[qi][i].Height = (e.Markers[qi][i].Height*float64(e.count[qi]) + o.Markers[qi][i].Height*float64(o.count[qi])) / total
		}
		e.count[qi] += o.count[qi]
	}
	return nil
}

// SizeBytes reports the published fixed size (DR-14 §14.1/§14.2): 5 markers
// x (position+height float64) x 3 quantiles = 240 B. The unexported
// bookkeeping this implementation adds (np, count, a transient init buffer)
// is deliberately excluded, since the published bound is about the steady-
// state marker set the register's memory model is built on.
func (e *P2Estimator) SizeBytes() int { return 240 }

type p2Snapshot struct {
	Markers [3][5][2]float64
	Np      [3][5]float64
	Count   [3]int
	Init    [3][]float64
}

func (e *P2Estimator) MarshalBinary() ([]byte, error) {
	var snap p2Snapshot
	for qi := 0; qi < 3; qi++ {
		for i := 0; i < 5; i++ {
			snap.Markers[qi][i] = [2]float64{e.Markers[qi][i].Position, e.Markers[qi][i].Height}
		}
		snap.Np[qi] = e.np[qi]
		snap.Count[qi] = e.count[qi]
		snap.Init[qi] = e.init[qi]
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(snap); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (e *P2Estimator) UnmarshalBinary(data []byte) error {
	var snap p2Snapshot
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&snap); err != nil {
		return err
	}
	for qi := 0; qi < 3; qi++ {
		for i := 0; i < 5; i++ {
			e.Markers[qi][i].Position = snap.Markers[qi][i][0]
			e.Markers[qi][i].Height = snap.Markers[qi][i][1]
		}
		e.np[qi] = snap.Np[qi]
		e.count[qi] = snap.Count[qi]
		e.init[qi] = snap.Init[qi]
	}
	return nil
}

// --- TDigest: bounded, mergeable sorted-value digest for the global slot. ---

// NewTDigest returns a TDigest bounded by compression (DR-14 §14.1: default
// 100, <= 512 B serialized target for the compact form -- see the
// tdigestMaxValues comment below for why this sorted-value-array
// implementation cannot actually reach that target without risking the 1%
// quantile-accuracy bound; SizeBytes() reports the real, larger footprint).
func NewTDigest(compression int) *TDigest {
	if compression <= 0 {
		compression = 100
	}
	return &TDigest{Compression: compression}
}

var _ QuantileEstimator = (*TDigest)(nil)

// tdigestMaxValues/compress's target (4096 / compression*20 = up to ~32KB
// retained pre-compress, settling to ~16KB steady-state at compression=100)
// are 30-60x the "<= 512 B serialized" target DR-14 §14.2's 91.9 MiB
// baseline-subtotal / 120 MiB total RSS budget is built on (512 B/key
// assumed for the global slot's t-digest, x 10 000 keys/tenant).
//
// A w12 review pass tried tightening this (smaller tdigestMaxValues/target)
// and found the 1% quantile-accuracy bound (AC-F05-1) is already marginal
// at these settings -- compress()'s evenly-spaced-from-index-0 downsampling
// has a systematic (not just random) bias that pushes p50 error close to
// 1% even at the current, looser bound (measured worst-case ~0.94% across
// 10 seeds at N=5000). Shrinking the retained set further pushed multiple
// seeds over 1%. Closing the real gap to 512 B needs an actual
// centroid-based t-digest (mean+weight pairs, proper cluster merging), not
// a smaller sorted-value array; that's a larger rewrite than this review's
// scope, and this type is already flagged NEEDS_CONTEXT above as
// under-specified/reconstructed by the register. Left at the original,
// accuracy-safe bound; SizeBytes() below was fixed instead to report the
// real retained size rather than hiding the gap behind a hardcoded 512.
const tdigestMaxValues = 4096

func (d *TDigest) Add(v float64) {
	d.values = insertSorted(d.values, v)
	if len(d.values) > tdigestMaxValues {
		d.compress()
	}
}

func insertSorted(s []float64, v float64) []float64 {
	i := sort.SearchFloat64s(s, v)
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

// compress downsamples by keeping evenly spaced values, bounding memory
// while preserving the overall shape closely enough for baseline use.
func (d *TDigest) compress() {
	target := d.Compression * 20
	if target <= 0 || target >= len(d.values) {
		return
	}
	step := float64(len(d.values)) / float64(target)
	out := make([]float64, 0, target)
	for f := 0.0; int(f) < len(d.values); f += step {
		out = append(out, d.values[int(f)])
	}
	d.values = out
}

func (d *TDigest) Quantile(q float64) float64 {
	if len(d.values) == 0 {
		return 0
	}
	idx := int(q * float64(len(d.values)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(d.values) {
		idx = len(d.values) - 1
	}
	return d.values[idx]
}

func (d *TDigest) Merge(other QuantileEstimator) error {
	o, ok := other.(*TDigest)
	if !ok {
		return fmt.Errorf("anomaly: TDigest.Merge: incompatible type %T", other)
	}
	for _, v := range o.values {
		d.values = insertSorted(d.values, v)
	}
	if len(d.values) > tdigestMaxValues {
		d.compress()
	}
	return nil
}

// SizeBytes reports the estimator's actual retained footprint (8 bytes per
// float64 value), not the published <= 512 B compact-form target (DR-14
// §14.1). It used to hardcode 512 unconditionally, which was simply false
// once any values had been added and made the published memory budget
// (DR-14 §14.2) unverifiable from this method; fixed in the w12 review
// pass so Stats()/monitoring see the real number. See the tdigestMaxValues
// comment for why this design can't fully close the gap to 512 B.
func (d *TDigest) SizeBytes() int { return len(d.values) * 8 }

type tdigestSnapshot struct {
	Compression int
	Values      []float64
}

func (d *TDigest) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(tdigestSnapshot{d.Compression, d.values}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (d *TDigest) UnmarshalBinary(data []byte) error {
	var snap tdigestSnapshot
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&snap); err != nil {
		return err
	}
	d.Compression = snap.Compression
	d.values = snap.Values
	return nil
}
