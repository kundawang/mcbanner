package mcbanner

// Constrained recipe search.
//
// The original genetic algorithm in ga.go searches the full pattern and color
// space with no restrictions. This file adds a second, independent search that
// is given a limited pool of dyes, a limited pool of patterns and a maximum
// number of layers. Every candidate, every mutation and every crossover is
// restricted to those resources, and the returned Recipe can be applied layer
// by layer and re-rendered reproducibly without any external tools.
//
// All randomness comes from a local math/rand.Rand seeded from
// SearchOptions.Seed, so equal inputs and equal seeds produce an identical
// result. Nothing here changes the behavior of FindBest or the package level
// random helpers.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand"
	"strings"
)

const (
	// MaxSearchLayers is the hard upper bound for searched recipes. A
	// Minecraft banner holds at most six layers.
	MaxSearchLayers = 6

	// Defaults for a constrained search.
	DefaultPopulationSize = 160
	DefaultGenerations    = 120
	DefaultMaxEvaluations = 40000

	// Hard caps. Requested values above these are clamped.
	HardMaxPopulationSize = 2000
	HardMaxGenerations    = 1000
	HardMaxEvaluations    = 500000

	// exhaustiveSpaceLimit is the largest recipe space that is searched
	// exhaustively instead of with the genetic algorithm. Enumeration is
	// deterministic and finds the shortest exact recipe, which is important
	// for the small "a few dyes, a few layers" scenarios.
	exhaustiveSpaceLimit = 20000

	// Fitnesses at or above this value are treated as an exact pixel match.
	exactFitness = 1.0 - 1e-9

	// Tolerance (0..1 per channel) when deciding whether a target color can
	// be produced from the allowed dye colors.
	drawableTolerance = 0.012
)

var (
	// ErrNoDyes means the allowed dye set was empty.
	ErrNoDyes = errors.New("mcbanner: no dyes were allowed")
	// ErrNoPatterns means the allowed pattern set was empty.
	ErrNoPatterns = errors.New("mcbanner: no patterns were allowed")
	// ErrZeroLayers means the maximum number of layers was zero (or negative).
	ErrZeroLayers = errors.New("mcbanner: the maximum number of layers must be at least one")
	// ErrLayerLimit means the requested layer count exceeds MaxSearchLayers.
	ErrLayerLimit = errors.New("mcbanner: too many layers requested")
	// ErrInvalidDye means a dye ID is outside the known range.
	ErrInvalidDye = errors.New("mcbanner: invalid dye ID")
	// ErrInvalidPattern means a pattern ID is outside the known range.
	ErrInvalidPattern = errors.New("mcbanner: invalid pattern ID")
	// ErrDuplicatePattern means the same pattern appears twice, either in the
	// allowed pattern set or in a recipe.
	ErrDuplicatePattern = errors.New("mcbanner: duplicate pattern")
	// ErrDisallowedLayer means a recipe uses a dye or pattern outside Limits.
	ErrDisallowedLayer = errors.New("mcbanner: layer is not allowed by the limits")
	// ErrNilTarget means no target image was given.
	ErrNilTarget = errors.New("mcbanner: nil target image")
	// ErrTargetSize means the target does not have the banner dimensions.
	ErrTargetSize = errors.New("mcbanner: target image must be BannerW x BannerH pixels")
	// ErrUndrawableTarget means the target can not be produced with the
	// allowed dyes/patterns (for example it contains a foreign color).
	ErrUndrawableTarget = errors.New("mcbanner: target can not be drawn with the allowed resources")
)

// constraintError adds context to a sentinel error while keeping it
// unwrappable, without relying on fmt.Errorf("%w") (the module declares
// go 1.11).
type constraintError struct {
	kind error
	msg  string
}

func (e *constraintError) Error() string {
	if e.msg == "" {
		return e.kind.Error()
	}
	return e.kind.Error() + ": " + e.msg
}

func (e *constraintError) Unwrap() error { return e.kind }

func constraintErrorf(kind error, format string, args ...interface{}) error {
	return &constraintError{kind: kind, msg: fmt.Sprintf(format, args...)}
}

// wrapError keeps the underlying decode error reachable without %w.
type wrapError struct {
	msg string
	err error
}

func (e *wrapError) Error() string { return e.msg + ": " + e.err.Error() }

func (e *wrapError) Unwrap() error { return e.err }

// Layer is one application of a pattern with a dye.
type Layer struct {
	Pattern int
	Dye     int
}

// Recipe is an ordered list of banner layers, from the background upwards.
type Recipe []Layer

// Validate checks IDs and rejects repeated pattern applications.
func (r Recipe) Validate() error {
	if len(r) == 0 {
		return ErrZeroLayers
	}
	seen := make(map[int]bool, len(r))
	for i, layer := range r {
		if !validPatternID(layer.Pattern) {
			return constraintErrorf(ErrInvalidPattern, "layer %d has pattern ID %d", i+1, layer.Pattern)
		}
		if !validDyeID(layer.Dye) {
			return constraintErrorf(ErrInvalidDye, "layer %d has dye ID %d", i+1, layer.Dye)
		}
		if seen[layer.Pattern] {
			return constraintErrorf(ErrDuplicatePattern, "pattern %q appears more than once (layer %d)",
				PatternName(layer.Pattern), i+1)
		}
		seen[layer.Pattern] = true
	}
	return nil
}

// Banner turns the recipe into a Banner that can be rendered.
func (r Recipe) Banner() *Banner {
	b := NewBanner()
	for i := range r {
		pat, err := NewPattern(r[i].Pattern, r[i].Dye)
		if err != nil {
			// Validate catches this; keep Banner total for convenience.
			continue
		}
		b.AddPattern(pat)
	}
	return b
}

// Steps returns a human readable, reproducible recipe, one instruction per
// layer. This is what a player needs to recreate the banner in game.
func (r Recipe) Steps() []string {
	steps := make([]string, len(r))
	for i, layer := range r {
		if layer.Pattern == PatternFull {
			steps[i] = fmt.Sprintf("%d. Start with a %s banner", i+1, ColorName(layer.Dye))
			continue
		}
		steps[i] = fmt.Sprintf("%d. Apply the %s pattern using %s dye",
			i+1, PatternName(layer.Pattern), ColorName(layer.Dye))
	}
	return steps
}

func (r Recipe) String() string {
	return strings.Join(r.Steps(), "\n")
}

// SVG renders the recipe as SVG. This uses the package's normal SVG drawing.
func (r Recipe) SVG() []byte {
	return r.Banner().SVG()
}

// PNG renders the recipe as PNG with the built-in renderer. It never invokes
// rsvg-convert, so the pixels are reproducible on any machine.
func (r Recipe) PNG() []byte {
	return RenderPNG(r.Banner())
}

// Pixels renders the recipe and returns the raw image.
func (r Recipe) Pixels() (*image.NRGBA, error) {
	return RenderRecipe(r)
}

// Limits describes the resources the search may use.
type Limits struct {
	// Dyes is the set of allowed dye IDs (that is, pattern colors).
	Dyes []int
	// Patterns is the set of allowed pattern IDs. Duplicates are rejected.
	Patterns []int
	// MaxLayers is the maximum recipe length, between 1 and MaxSearchLayers.
	MaxLayers int
}

type normalizedLimits struct {
	dyes      []int
	patterns  []int
	maxLayers int
}

func (l Limits) normalize() (normalizedLimits, error) {
	if l.MaxLayers == 0 || l.MaxLayers < 0 {
		return normalizedLimits{}, ErrZeroLayers
	}
	if l.MaxLayers > MaxSearchLayers {
		return normalizedLimits{}, constraintErrorf(ErrLayerLimit, "%d (maximum is %d)", l.MaxLayers, MaxSearchLayers)
	}
	if len(l.Dyes) == 0 {
		return normalizedLimits{}, ErrNoDyes
	}
	if len(l.Patterns) == 0 {
		return normalizedLimits{}, ErrNoPatterns
	}

	dyes := make([]int, 0, len(l.Dyes))
	seenDyes := make(map[int]bool, len(l.Dyes))
	for _, d := range l.Dyes {
		if !validDyeID(d) {
			return normalizedLimits{}, constraintErrorf(ErrInvalidDye, "%d", d)
		}
		// Duplicate dyes do not create ambiguous recipes, so they are
		// silently folded into one entry.
		if !seenDyes[d] {
			dyes = append(dyes, d)
			seenDyes[d] = true
		}
	}

	patterns := make([]int, 0, len(l.Patterns))
	seenPatterns := make(map[int]bool, len(l.Patterns))
	for _, p := range l.Patterns {
		if !validPatternID(p) {
			return normalizedLimits{}, constraintErrorf(ErrInvalidPattern, "%d", p)
		}
		if seenPatterns[p] {
			return normalizedLimits{}, constraintErrorf(ErrDuplicatePattern, "pattern %q", PatternName(p))
		}
		patterns = append(patterns, p)
		seenPatterns[p] = true
	}

	maxLayers := l.MaxLayers
	// Recipes may not repeat a pattern, so more layers than allowed patterns
	// can never be used.
	if maxLayers > len(patterns) {
		maxLayers = len(patterns)
	}
	return normalizedLimits{dyes: dyes, patterns: patterns, maxLayers: maxLayers}, nil
}

func (n normalizedLimits) patternAllowed(p int) bool {
	for _, allowed := range n.patterns {
		if allowed == p {
			return true
		}
	}
	return false
}

func (n normalizedLimits) dyeAllowed(d int) bool {
	for _, allowed := range n.dyes {
		if allowed == d {
			return true
		}
	}
	return false
}

func validPatternID(p int) bool {
	return p >= PatternLowerThird && p <= PatternFull
}

func validDyeID(d int) bool {
	return d >= ColorWhite && d <= ColorBrightWhite
}

// SearchStatus is the outcome of a constrained search.
type SearchStatus int

const (
	// StatusFound means a recipe with an exact pixel match was found.
	StatusFound SearchStatus = iota
	// StatusBestEffort means the generation limit was reached without an
	// exact match. The best recipe found is still returned.
	StatusBestEffort
	// StatusBudgetExhausted means MaxEvaluations was reached before an exact
	// match was found. The best recipe found so far is returned.
	StatusBudgetExhausted
)

func (s SearchStatus) String() string {
	switch s {
	case StatusFound:
		return "found"
	case StatusBestEffort:
		return "best-effort"
	case StatusBudgetExhausted:
		return "budget-exhausted"
	default:
		return "unknown"
	}
}

// SearchOptions controls the constrained search. Zero values select the
// corresponding default. Values above the hard caps are clamped; the effective
// values are reported in SearchResult.
type SearchOptions struct {
	// Seed makes the search deterministic.
	Seed int64
	// PopulationSize is the number of candidates per generation.
	PopulationSize int
	// Generations is the maximum number of generations.
	Generations int
	// MaxEvaluations is the total fitness evaluation budget. It includes the
	// initial population and recipe minimization. It is a hard stop.
	MaxEvaluations int
	// MutationRate is the probability of applying a restricted mutation.
	MutationRate float64
	// CrossoverRate is the probability of applying a restricted crossover.
	CrossoverRate float64
	// NewCandidateRate is the probability of replacing a child with a new
	// random restricted candidate.
	NewCandidateRate float64
	// ForceGenetic disables the exhaustive search path. It is useful for
	// exercising the genetic operators on small fixture spaces.
	ForceGenetic bool
}

type normalizedOptions struct {
	seed               int64
	populationSize     int
	generations        int
	maxEvaluations     int
	mutationRate       float64
	crossoverRate      float64
	newCandidateRate   float64
	forceGenetic       bool
	clampedPopulation  bool
	clampedGenerations bool
	clampedEvaluations bool
}

func (o SearchOptions) normalize() normalizedOptions {
	n := normalizedOptions{
		seed:             o.Seed,
		populationSize:   o.PopulationSize,
		generations:      o.Generations,
		maxEvaluations:   o.MaxEvaluations,
		mutationRate:     o.MutationRate,
		crossoverRate:    o.CrossoverRate,
		newCandidateRate: o.NewCandidateRate,
		forceGenetic:     o.ForceGenetic,
	}
	if n.populationSize <= 0 {
		n.populationSize = DefaultPopulationSize
	} else if n.populationSize > HardMaxPopulationSize {
		n.populationSize = HardMaxPopulationSize
		n.clampedPopulation = true
	}
	if n.generations <= 0 {
		n.generations = DefaultGenerations
	} else if n.generations > HardMaxGenerations {
		n.generations = HardMaxGenerations
		n.clampedGenerations = true
	}
	if n.maxEvaluations <= 0 {
		n.maxEvaluations = DefaultMaxEvaluations
	} else if n.maxEvaluations > HardMaxEvaluations {
		n.maxEvaluations = HardMaxEvaluations
		n.clampedEvaluations = true
	}
	if n.mutationRate < 0 || n.mutationRate > 1 {
		n.mutationRate = 0.35
	} else if n.mutationRate == 0 {
		n.mutationRate = 0.35
	}
	if n.crossoverRate < 0 || n.crossoverRate > 1 {
		n.crossoverRate = 0.25
	} else if n.crossoverRate == 0 {
		n.crossoverRate = 0.25
	}
	if n.newCandidateRate < 0 || n.newCandidateRate > 1 {
		n.newCandidateRate = 0.1
	} else if n.newCandidateRate == 0 {
		n.newCandidateRate = 0.1
	}
	return n
}

// SearchResult is the outcome of FindConstrainedRecipe.
type SearchResult struct {
	// Recipe is the best recipe found. It always obeys Limits.
	Recipe Recipe
	// Fitness is the pixel similarity, where 1 means an exact match.
	Fitness float64
	// Status reports why the search stopped.
	Status SearchStatus
	// Comparisons is the number of fitness evaluations that were performed.
	Comparisons int
	// Generations is the number of generations that were run (0 for an
	// exhaustive search that found a recipe immediately).
	Generations int
	// Seed is the seed that was used.
	Seed int64
	// EffectiveMaxLayers is the layer cap after accounting for the number of
	// distinct allowed patterns.
	EffectiveMaxLayers int
	// PopulationSize, MaxGenerations and MaxEvaluations are the effective
	// (possibly clamped) search limits.
	PopulationSize int
	MaxGenerations int
	MaxEvaluations int
	// Exhaustive reports whether the exhaustive path was used.
	Exhaustive bool
	// Clamped reports whether a requested option was reduced to a hard cap.
	Clamped bool
}

// Found reports whether an exact pixel match was found.
func (r SearchResult) Found() bool {
	return r.Status == StatusFound && len(r.Recipe) > 0
}

// Steps returns the reproducible per-layer recipe.
func (r SearchResult) Steps() []string { return r.Recipe.Steps() }

// PreviewPNG returns the reproducible preview as PNG bytes.
func (r SearchResult) PreviewPNG() []byte { return r.Recipe.PNG() }

// PreviewSVG returns the reproducible preview as SVG bytes.
func (r SearchResult) PreviewSVG() []byte { return r.Recipe.SVG() }

func (r SearchResult) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "status: %s\n", r.Status)
	fmt.Fprintf(&b, "fitness: %.6f\n", r.Fitness)
	fmt.Fprintf(&b, "layers: %d/%d\n", len(r.Recipe), r.EffectiveMaxLayers)
	fmt.Fprintf(&b, "comparisons: %d (budget %d)\n", r.Comparisons, r.MaxEvaluations)
	b.WriteString("recipe:\n")
	b.WriteString(r.Recipe.String())
	return b.String()
}

// targetImage is the target flattened to opaque NRGBA pixels.
type targetImage struct {
	pix []color.NRGBA
}

func newTargetImage(img image.Image) (*targetImage, error) {
	if img == nil {
		return nil, ErrNilTarget
	}
	bounds := img.Bounds()
	if bounds.Dx() != BannerW || bounds.Dy() != BannerH {
		return nil, constraintErrorf(ErrTargetSize, "got %dx%d, want %dx%d",
			bounds.Dx(), bounds.Dy(), BannerW, BannerH)
	}
	pix := make([]color.NRGBA, BannerW*BannerH)
	for y := 0; y < BannerH; y++ {
		for x := 0; x < BannerW; x++ {
			pix[y*BannerW+x] = opaqueOverWhite(img.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return &targetImage{pix: pix}, nil
}

func opaqueOverWhite(c color.Color) color.NRGBA {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	a := float64(n.A) / 255.0
	return color.NRGBA{
		R: uint8(math.Round(float64(n.R)*a + 255.0*(1.0-a))),
		G: uint8(math.Round(float64(n.G)*a + 255.0*(1.0-a))),
		B: uint8(math.Round(float64(n.B)*a + 255.0*(1.0-a))),
		A: 255,
	}
}

// fitness compares the target with a rendered candidate, returning 0..1.
func (t *targetImage) fitness(img *image.NRGBA) float64 {
	var total float64
	for y := 0; y < BannerH; y++ {
		for x := 0; x < BannerW; x++ {
			want := t.pix[y*BannerW+x]
			got := img.NRGBAAt(x, y)
			dr := math.Abs(float64(want.R) - float64(got.R))
			dg := math.Abs(float64(want.G) - float64(got.G))
			db := math.Abs(float64(want.B) - float64(got.B))
			total += ((dr + dg + db) / 3.0) / 255.0
		}
	}
	return 1.0 - total/float64(BannerW*BannerH)
}

type rgb struct{ r, g, b float64 }

func hexToRGB(s string) (rgb, bool) {
	c := parseColorString(s)
	if c.a <= 0 {
		return rgb{}, false
	}
	return rgb{c.r, c.g, c.b}, true
}

func whiteRGB() rgb { return rgb{1, 1, 1} }

// drawablePalette returns the colors that a single opaque layer can paint:
// white (the unpainted background) followed by every allowed dye.
func drawablePalette(n normalizedLimits) []rgb {
	palette := []rgb{whiteRGB()}
	for _, dye := range n.dyes {
		hex, ok := colors[dye]
		if !ok {
			continue
		}
		if col, ok := hexToRGB(hex); ok {
			palette = append(palette, col)
		}
	}
	return palette
}

func nearRGB(a, b rgb, tolerance float64) bool {
	return math.Abs(a.r-b.r) <= tolerance &&
		math.Abs(a.g-b.g) <= tolerance &&
		math.Abs(a.b-b.b) <= tolerance
}

func nearAny(c rgb, palette []rgb, tolerance float64) bool {
	for _, p := range palette {
		if nearRGB(c, p, tolerance) {
			return true
		}
	}
	return false
}

func pointSegmentDistance(p, a, b rgb) float64 {
	dx, dy, dz := b.r-a.r, b.g-a.g, b.b-a.b
	lengthSq := dx*dx + dy*dy + dz*dz
	t := 0.0
	if lengthSq > 0 {
		t = ((p.r-a.r)*dx + (p.g-a.g)*dy + (p.b-a.b)*dz) / lengthSq
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
	}
	cr, cg, cb := a.r+t*dx, a.g+t*dy, a.b+t*dz
	dr, dg, db := p.r-cr, p.g-cg, p.b-cb
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

func onAnySegment(c rgb, palette []rgb, tolerance float64) bool {
	for i := 0; i < len(palette); i++ {
		for j := i + 1; j < len(palette); j++ {
			if pointSegmentDistance(c, palette[i], palette[j]) <= tolerance {
				return true
			}
		}
	}
	return false
}

func containsPattern(patterns []int, want int) bool {
	for _, p := range patterns {
		if p == want {
			return true
		}
	}
	return false
}

// checkDrawable rejects targets that can clearly not be produced with the
// allowed resources.
//
// Every pixel of a rendered banner is a convex combination of white (the
// unpainted background) and the allowed dye colors: each layer only adds more
// coverage of one of those colors over what is already there. A pixel that is
// outside the convex hull of those colors therefore can not be produced with
// any number of layers, and the target is reported as undrawable. Pixels
// inside the hull (including antialiased edges and three-color corners) are
// accepted; if they still need more layers than allowed, the search reports a
// best-effort result instead.
func checkDrawable(t *targetImage, n normalizedLimits) error {
	palette := drawablePalette(n)

	for y := 0; y < BannerH; y++ {
		for x := 0; x < BannerW; x++ {
			p := t.pix[y*BannerW+x]
			c := rgb{float64(p.R) / 255.0, float64(p.G) / 255.0, float64(p.B) / 255.0}
			if insideColorHull(c, palette, drawableTolerance) {
				continue
			}
			return constraintErrorf(ErrUndrawableTarget, "pixel (%d,%d) is #%02x%02x%02x, which is outside the range of colors the allowed dyes can produce",
				x, y, p.R, p.G, p.B)
		}
	}
	return nil
}

// insideColorHull reports whether c lies inside the convex hull of palette
// (within tolerance). By Carathéodory's theorem in R^3 it is enough to test
// every point, segment, triangle and tetrahedron spanned by palette points.
func insideColorHull(c rgb, palette []rgb, tolerance float64) bool {
	for _, p := range palette {
		if nearRGB(c, p, tolerance) {
			return true
		}
	}
	for i := 0; i < len(palette); i++ {
		for j := i + 1; j < len(palette); j++ {
			if pointSegmentDistance(c, palette[i], palette[j]) <= tolerance {
				return true
			}
		}
	}
	for i := 0; i < len(palette); i++ {
		for j := i + 1; j < len(palette); j++ {
			for k := j + 1; k < len(palette); k++ {
				if pointInColorTriangle(c, palette[i], palette[j], palette[k], tolerance) {
					return true
				}
			}
		}
	}
	for i := 0; i < len(palette); i++ {
		for j := i + 1; j < len(palette); j++ {
			for k := j + 1; k < len(palette); k++ {
				for l := k + 1; l < len(palette); l++ {
					if pointInColorTetrahedron(c, palette[i], palette[j], palette[k], palette[l], tolerance) {
						return true
					}
				}
			}
		}
	}
	return false
}

func pointInColorTetrahedron(p, a, b, c, d rgb, tolerance float64) bool {
	// Barycentric coordinates via signed volumes.
	det := det3(
		a.r-d.r, b.r-d.r, c.r-d.r,
		a.g-d.g, b.g-d.g, c.g-d.g,
		a.b-d.b, b.b-d.b, c.b-d.b,
	)
	if math.Abs(det) < 1e-9 {
		return false
	}
	l1 := det3(
		p.r-d.r, b.r-d.r, c.r-d.r,
		p.g-d.g, b.g-d.g, c.g-d.g,
		p.b-d.b, b.b-d.b, c.b-d.b,
	) / det
	l2 := det3(
		a.r-d.r, p.r-d.r, c.r-d.r,
		a.g-d.g, p.g-d.g, c.g-d.g,
		a.b-d.b, p.b-d.b, c.b-d.b,
	) / det
	l3 := det3(
		a.r-d.r, b.r-d.r, p.r-d.r,
		a.g-d.g, b.g-d.g, p.g-d.g,
		a.b-d.b, b.b-d.b, p.b-d.b,
	) / det
	l4 := 1 - l1 - l2 - l3
	if l1 < -tolerance || l2 < -tolerance || l3 < -tolerance || l4 < -tolerance {
		return false
	}
	// The determinants already include the blue channel; no extra check needed.
	return true
}

func det3(a11, a12, a13, a21, a22, a23, a31, a32, a33 float64) float64 {
	return a11*(a22*a33-a23*a32) - a12*(a21*a33-a23*a31) + a13*(a21*a32-a22*a31)
}

func pointInColorTriangle(p, a, b, c rgb, tolerance float64) bool {
	// Barycentric coordinates of p.
	det := (b.g-c.g)*(a.r-c.r) + (c.r-b.r)*(a.g-c.g)
	if math.Abs(det) < 1e-9 {
		return false
	}
	l1 := ((b.g-c.g)*(p.r-c.r) + (c.r-b.r)*(p.g-c.g)) / det
	l2 := ((c.g-a.g)*(p.r-c.r) + (a.r-c.r)*(p.g-c.g)) / det
	l3 := 1 - l1 - l2
	if l1 < -tolerance || l2 < -tolerance || l3 < -tolerance {
		return false
	}
	// The 2D barycentric test alone ignores the blue channel, so also check
	// that the interpolated blue value matches.
	blue := l1*a.b + l2*b.b + l3*c.b
	return math.Abs(blue-p.b) <= tolerance
}

// FindConstrainedRecipe searches for the best recipe that uses only the
// allowed dyes and patterns and at most Limits.MaxLayers layers.
//
// Input errors are returned as errors wrapping ErrNoDyes, ErrNoPatterns,
// ErrZeroLayers, ErrLayerLimit, ErrDuplicatePattern, ErrInvalidDye,
// ErrInvalidPattern, ErrNilTarget, ErrTargetSize or ErrUndrawableTarget.
// Reaching the evaluation budget is not an error: it is reported through
// SearchResult.Status == StatusBudgetExhausted.
func FindConstrainedRecipe(target image.Image, limits Limits, opts SearchOptions) (SearchResult, error) {
	nlimits, err := limits.normalize()
	if err != nil {
		return SearchResult{}, err
	}
	targetPixels, err := newTargetImage(target)
	if err != nil {
		return SearchResult{}, err
	}
	if err := checkDrawable(targetPixels, nlimits); err != nil {
		return SearchResult{}, err
	}
	nopts := opts.normalize()

	result := SearchResult{
		Seed:               nopts.seed,
		EffectiveMaxLayers: nlimits.maxLayers,
		PopulationSize:     nopts.populationSize,
		MaxGenerations:     nopts.generations,
		MaxEvaluations:     nopts.maxEvaluations,
		Clamped:            nopts.clampedPopulation || nopts.clampedGenerations || nopts.clampedEvaluations,
	}

	// Small spaces are enumerated: that is deterministic and finds the
	// shortest exact recipe. The evaluation budget still applies, so an
	// exhaustive run can stop early with StatusBudgetExhausted.
	space := recipeSpaceSize(nlimits, exhaustiveSpaceLimit)
	var best Recipe
	var bestFitness float64
	var comparisons int
	var generations int

	if !nopts.forceGenetic && space <= exhaustiveSpaceLimit {
		result.Exhaustive = true
		best, bestFitness, comparisons, result.Status = exhaustiveSearch(targetPixels, nlimits, nopts.maxEvaluations)
	} else {
		best, bestFitness, comparisons, generations, result.Status = geneticSearch(targetPixels, nlimits, nopts)
	}

	// Try to shorten an exact recipe without changing its pixels. This is
	// deterministic and only removes layers, so it keeps the recipe valid.
	if result.Status == StatusFound && len(best) > 1 && comparisons < nopts.maxEvaluations {
		pruned, prunedComparisons := pruneRecipe(best, targetPixels, nopts.maxEvaluations-comparisons)
		best = pruned
		comparisons += prunedComparisons
	}

	result.Recipe = best
	result.Fitness = bestFitness
	result.Comparisons = comparisons
	result.Generations = generations
	return result, nil
}

// FindConstrainedRecipeFromPNG is FindConstrainedRecipe for encoded PNG bytes.
func FindConstrainedRecipeFromPNG(pngBytes []byte, limits Limits, opts SearchOptions) (SearchResult, error) {
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return SearchResult{}, &wrapError{msg: "mcbanner: could not decode target PNG", err: err}
	}
	return FindConstrainedRecipe(img, limits, opts)
}

// recipeSpaceSize counts the recipes with distinct patterns and length in
// [1, maxLayers]. It saturates at stop+1 to avoid large exponentiation.
func recipeSpaceSize(n normalizedLimits, stop int64) int64 {
	var total, permutations int64 = 0, 1
	for length := 1; length <= n.maxLayers; length++ {
		permutations *= int64(len(n.patterns) - length + 1)
		dyeCombinations := int64(1)
		for i := 0; i < length; i++ {
			dyeCombinations *= int64(len(n.dyes))
			if dyeCombinations > stop {
				dyeCombinations = stop
				break
			}
		}
		total += permutations * dyeCombinations
		if total > stop {
			return stop + 1
		}
	}
	return total
}

// exhaustiveSearch enumerates recipes from the shortest upwards. It returns
// the first exact recipe, otherwise the best recipe seen.
func exhaustiveSearch(target *targetImage, n normalizedLimits, budget int) (Recipe, float64, int, SearchStatus) {
	var (
		best        Recipe
		bestFitness float64 = -1
		comparisons int
		stop        bool
		status      = StatusBestEffort
		used        = make([]bool, len(n.patterns))
		current     Recipe
	)

	// evaluate records one candidate. It returns true when the search should
	// stop immediately (exact match or exhausted budget).
	evaluate := func(rec Recipe) bool {
		if comparisons >= budget {
			stop = true
			status = StatusBudgetExhausted
			return true
		}
		copyRecipe := append(Recipe(nil), rec...)
		fitness := target.fitness(RenderPixels(copyRecipe.Banner()))
		comparisons++
		if fitness > bestFitness {
			bestFitness = fitness
			best = copyRecipe
		}
		if fitness >= exactFitness {
			stop = true
			status = StatusFound
			return true
		}
		return false
	}

	var enumerate func(length int)
	enumerate = func(length int) {
		if stop {
			return
		}
		if len(current) == length {
			if evaluate(current) {
				return
			}
			if comparisons >= budget {
				stop = true
				status = StatusBudgetExhausted
			}
			return
		}
		for pi, pattern := range n.patterns {
			if stop {
				return
			}
			if used[pi] {
				continue
			}
			used[pi] = true
			for _, dye := range n.dyes {
				if stop {
					break
				}
				current = append(current, Layer{Pattern: pattern, Dye: dye})
				enumerate(length)
				current = current[:len(current)-1]
				if comparisons >= budget {
					stop = true
					status = StatusBudgetExhausted
				}
			}
			used[pi] = false
		}
	}

	for length := 1; length <= n.maxLayers && !stop; length++ {
		current = current[:0]
		for i := range used {
			used[i] = false
		}
		enumerate(length)
	}
	if bestFitness < 0 {
		bestFitness = 0
	}
	return best, bestFitness, comparisons, status
}

// gaCandidate is a recipe together with its fitness.
type gaCandidate struct {
	recipe  Recipe
	fitness float64
}

// geneticSearch runs a constrained genetic algorithm. Every operator draws
// from normalizedLimits and never from the global pattern/color generators.
func geneticSearch(target *targetImage, n normalizedLimits, opts normalizedOptions) (Recipe, float64, int, int, SearchStatus) {
	rng := rand.New(rand.NewSource(opts.seed))

	evaluate := func(rec Recipe) gaCandidate {
		return gaCandidate{recipe: rec, fitness: target.fitness(RenderPixels(rec.Banner()))}
	}

	var (
		comparisons int
		best        Recipe
		bestFitness float64 = -1
		status              = StatusBestEffort
	)
	keepBest := func(c gaCandidate) bool {
		if c.fitness > bestFitness {
			bestFitness = c.fitness
			best = append(Recipe(nil), c.recipe...)
		}
		return c.fitness >= exactFitness
	}

	pop := make([]gaCandidate, 0, opts.populationSize)
	for len(pop) < opts.populationSize {
		if comparisons >= opts.maxEvaluations {
			status = StatusBudgetExhausted
			break
		}
		rec := newRandomConstrainedRecipe(rng, n)
		c := evaluate(rec)
		comparisons++
		if keepBest(c) {
			return best, bestFitness, comparisons, 0, StatusFound
		}
		pop = append(pop, c)
	}

	generations := 0
	for generations < opts.generations && status != StatusBudgetExhausted && status != StatusFound {
		generations++
		// Elitism keeps the best candidates unchanged.
		ordered := make([]gaCandidate, len(pop))
		copy(ordered, pop)
		sortCandidates(ordered)
		eliteCount := len(ordered) / 10
		if eliteCount < 1 {
			eliteCount = 1
		}
		next := make([]gaCandidate, 0, opts.populationSize)
		for i := 0; i < eliteCount && i < len(ordered); i++ {
			next = append(next, ordered[i])
		}
		for len(next) < opts.populationSize {
			if comparisons >= opts.maxEvaluations {
				status = StatusBudgetExhausted
				break
			}
			parent := tournamentSelect(rng, ordered)
			child := append(Recipe(nil), parent.recipe...)
			if rng.Float64() < opts.crossoverRate {
				other := tournamentSelect(rng, ordered)
				child = crossoverConstrained(child, other.recipe, rng, n)
			}
			if rng.Float64() < opts.mutationRate {
				child = mutateConstrained(child, rng, n)
			}
			if rng.Float64() < opts.newCandidateRate {
				child = newRandomConstrainedRecipe(rng, n)
			}
			child = sanitizeRecipe(child, rng, n)
			c := evaluate(child)
			comparisons++
			if keepBest(c) {
				status = StatusFound
				break
			}
			next = append(next, c)
		}
		pop = next
	}
	if bestFitness < 0 {
		bestFitness = 0
	}
	if status != StatusFound && comparisons >= opts.maxEvaluations {
		status = StatusBudgetExhausted
	}
	return best, bestFitness, comparisons, generations, status
}

func sortCandidates(candidates []gaCandidate) {
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j].fitness > candidates[j-1].fitness; j-- {
			candidates[j], candidates[j-1] = candidates[j-1], candidates[j]
		}
	}
}

func tournamentSelect(rng *rand.Rand, population []gaCandidate) gaCandidate {
	best := population[rng.Intn(len(population))]
	for i := 0; i < 2; i++ {
		challenger := population[rng.Intn(len(population))]
		if challenger.fitness > best.fitness {
			best = challenger
		}
	}
	return best
}

func randomDye(rng *rand.Rand, n normalizedLimits) int {
	return n.dyes[rng.Intn(len(n.dyes))]
}

// newRandomConstrainedRecipe creates a recipe of length 1..maxLayers using
// only allowed, distinct patterns and allowed dyes.
func newRandomConstrainedRecipe(rng *rand.Rand, n normalizedLimits) Recipe {
	length := 1 + rng.Intn(n.maxLayers)
	perm := rng.Perm(len(n.patterns))[:length]
	recipe := make(Recipe, length)
	for i, pi := range perm {
		recipe[i] = Layer{Pattern: n.patterns[pi], Dye: randomDye(rng, n)}
	}
	return recipe
}

// unusedPatterns returns the allowed patterns that are not in the recipe.
func unusedPatterns(recipe Recipe, n normalizedLimits) []int {
	used := make(map[int]bool, len(recipe))
	for _, layer := range recipe {
		used[layer.Pattern] = true
	}
	unused := make([]int, 0, len(n.patterns))
	for _, p := range n.patterns {
		if !used[p] {
			unused = append(unused, p)
		}
	}
	return unused
}

// mutateConstrained changes a recipe while staying inside the allowed
// resources. It never introduces a duplicate pattern.
func mutateConstrained(recipe Recipe, rng *rand.Rand, n normalizedLimits) Recipe {
	out := append(Recipe(nil), recipe...)
	unused := unusedPatterns(out, n)

	// Pick an operation that is actually possible.
	type mutation int
	operations := []mutation{0} // change a dye
	if len(unused) > 0 {
		operations = append(operations, 1) // insert a layer
		if len(out) > 0 {
			operations = append(operations, 3) // replace a pattern
		}
	}
	if len(out) > 1 {
		operations = append(operations, 2) // remove a layer
	}
	op := operations[rng.Intn(len(operations))]

	switch op {
	case 0:
		if len(out) > 0 {
			i := rng.Intn(len(out))
			out[i].Dye = randomDye(rng, n)
		}
	case 1:
		if len(unused) > 0 && len(out) < n.maxLayers {
			pattern := unused[rng.Intn(len(unused))]
			pos := rng.Intn(len(out) + 1)
			layer := Layer{Pattern: pattern, Dye: randomDye(rng, n)}
			out = append(out, Layer{})
			copy(out[pos+1:], out[pos:])
			out[pos] = layer
		}
	case 2:
		if len(out) > 1 {
			i := rng.Intn(len(out))
			out = append(out[:i], out[i+1:]...)
		}
	case 3:
		if len(unused) > 0 && len(out) > 0 {
			i := rng.Intn(len(out))
			out[i].Pattern = unused[rng.Intn(len(unused))]
			out[i].Dye = randomDye(rng, n)
		}
	}
	return out
}

// crossoverConstrained takes a prefix from a and appends unused, allowed
// patterns from b. The result never exceeds maxLayers and never repeats a
// pattern.
func crossoverConstrained(a, b Recipe, rng *rand.Rand, n normalizedLimits) Recipe {
	if len(a) == 0 {
		return sanitizeRecipe(append(Recipe(nil), b...), rng, n)
	}
	maxCut := len(a)
	if maxCut > n.maxLayers {
		maxCut = n.maxLayers
	}
	cut := 1 + rng.Intn(maxCut)
	child := append(Recipe(nil), a[:cut]...)
	if len(child) > n.maxLayers {
		child = child[:n.maxLayers]
	}
	used := make(map[int]bool, len(child))
	for _, layer := range child {
		used[layer.Pattern] = true
	}
	for _, layer := range b {
		if len(child) >= n.maxLayers {
			break
		}
		if used[layer.Pattern] || !n.patternAllowed(layer.Pattern) || !n.dyeAllowed(layer.Dye) {
			continue
		}
		child = append(child, layer)
		used[layer.Pattern] = true
	}
	return sanitizeRecipe(child, rng, n)
}

// sanitizeRecipe enforces the limits on an arbitrary recipe. It is used at
// the end of crossover so the restrictions also apply to inherited genes.
func sanitizeRecipe(recipe Recipe, rng *rand.Rand, n normalizedLimits) Recipe {
	out := make(Recipe, 0, len(recipe))
	seen := make(map[int]bool, len(recipe))
	for _, layer := range recipe {
		if len(out) >= n.maxLayers {
			break
		}
		if !n.patternAllowed(layer.Pattern) {
			continue
		}
		if seen[layer.Pattern] {
			continue
		}
		if !n.dyeAllowed(layer.Dye) {
			continue
		}
		out = append(out, layer)
		seen[layer.Pattern] = true
	}
	if len(out) == 0 {
		return newRandomConstrainedRecipe(rng, n)
	}
	return out
}

// pruneRecipe removes layers while the rendered pixels stay exactly the same
// as the target. It returns the number of fitness evaluations used.
func pruneRecipe(recipe Recipe, target *targetImage, budget int) (Recipe, int) {
	best := append(Recipe(nil), recipe...)
	comparisons := 0
	for i := len(best) - 1; i >= 0 && comparisons < budget; i-- {
		candidate := make(Recipe, 0, len(best)-1)
		candidate = append(candidate, best[:i]...)
		candidate = append(candidate, best[i+1:]...)
		if len(candidate) == 0 {
			continue
		}
		fitness := target.fitness(RenderPixels(candidate.Banner()))
		comparisons++
		if fitness >= exactFitness {
			best = candidate
		}
	}
	return best, comparisons
}
