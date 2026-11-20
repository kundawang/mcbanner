package mcbanner

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"testing"
)

// --- fixtures -------------------------------------------------------------

// twoColorTarget paints the top half with dyeA and the bottom half with dyeB,
// the colors produced by the banner's own renderer for those dyes.
func twoColorTarget(dyeTop, dyeBottom int) *image.NRGBA {
	top := solidDyeColor(dyeTop)
	bottom := solidDyeColor(dyeBottom)
	img := image.NewNRGBA(image.Rect(0, 0, BannerW, BannerH))
	for y := 0; y < BannerH; y++ {
		for x := 0; x < BannerW; x++ {
			if y < BannerH/2 {
				img.SetNRGBA(x, y, top)
			} else {
				img.SetNRGBA(x, y, bottom)
			}
		}
	}
	return img
}

// solidDyeColor renders a single PatternFull layer and reads back the exact
// pixel color, so fixtures use the very same colors as the search.
func solidDyeColor(dye int) color.NRGBA {
	b := NewBanner()
	p, err := NewPattern(PatternFull, dye)
	if err != nil {
		panic(err)
	}
	b.AddPattern(p)
	return RenderPixels(b).NRGBAAt(0, 0)
}

// patternTarget renders an arbitrary recipe as a target image.
func patternTarget(t *testing.T, rec Recipe) *image.NRGBA {
	t.Helper()
	img, err := RenderRecipe(rec)
	if err != nil {
		t.Fatalf("RenderRecipe(%v): %v", rec, err)
	}
	return img
}

// threeDyes is the scenario from the report: three dyes, four layers.
var threeDyes = []int{ColorRed, ColorYellow, ColorBlue}

func fourPatterns() []int {
	return []int{PatternFull, PatternUpperHalf, PatternLowerHalf, PatternCircle}
}

func recipeFitsLimits(t *testing.T, rec Recipe, limits Limits) {
	t.Helper()
	if len(rec) == 0 {
		t.Fatalf("recipe is empty")
	}
	if len(rec) > limits.MaxLayers {
		t.Fatalf("recipe has %d layers, limit is %d", len(rec), limits.MaxLayers)
	}
	dyeAllowed := make(map[int]bool)
	for _, d := range limits.Dyes {
		dyeAllowed[d] = true
	}
	patternAllowed := make(map[int]bool)
	for _, p := range limits.Patterns {
		patternAllowed[p] = true
	}
	seen := make(map[int]bool)
	for i, layer := range rec {
		if !dyeAllowed[layer.Dye] {
			t.Fatalf("layer %d uses disallowed dye %d", i+1, layer.Dye)
		}
		if !patternAllowed[layer.Pattern] {
			t.Fatalf("layer %d uses disallowed pattern %d", i+1, layer.Pattern)
		}
		if seen[layer.Pattern] {
			t.Fatalf("layer %d repeats pattern %q", i+1, PatternName(layer.Pattern))
		}
		seen[layer.Pattern] = true
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("recipe does not validate: %v", err)
	}
}

func sameImage(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
		for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
			r1, g1, b1, _ := a.At(x, y).RGBA()
			r2, g2, b2, _ := b.At(x, y).RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 {
				return false
			}
		}
	}
	return true
}

// --- requirement 1 & 4: two-color target with a limited dye fixture -------

func TestConstrainedTwoColorFixture(t *testing.T) {
	limits := Limits{
		Dyes:      []int{ColorRed, ColorYellow},
		Patterns:  []int{PatternFull, PatternUpperHalf},
		MaxLayers: 2,
	}
	// Target produced from a valid, allowed recipe.
	want := Recipe{
		{Pattern: PatternFull, Dye: ColorYellow},
		{Pattern: PatternUpperHalf, Dye: ColorRed},
	}
	target := patternTarget(t, want)

	result, err := FindConstrainedRecipe(target, limits, SearchOptions{Seed: 7})
	if err != nil {
		t.Fatalf("FindConstrainedRecipe: %v", err)
	}
	if !result.Found() {
		t.Fatalf("expected an exact recipe, got status %s (fitness %.6f)", result.Status, result.Fitness)
	}
	recipeFitsLimits(t, result.Recipe, limits)
	if result.Fitness < exactFitness {
		t.Fatalf("fitness %.6f is not exact", result.Fitness)
	}

	// Level-by-level legality: the recipe validates and every step names a
	// real dye and pattern.
	steps := result.Steps()
	if len(steps) != len(result.Recipe) {
		t.Fatalf("got %d steps for %d layers", len(steps), len(result.Recipe))
	}
	for i, layer := range result.Recipe {
		if ColorName(layer.Dye) == "Unknown color" {
			t.Fatalf("step %d has unknown dye %d", i+1, layer.Dye)
		}
		if PatternName(layer.Pattern) == "Unknown Pattern" {
			t.Fatalf("step %d has unknown pattern %d", i+1, layer.Pattern)
		}
	}

	// Reproduced pixels: re-rendering the recipe must match the target
	// exactly, and match a fresh Banner built from the recipe.
	img, err := result.Recipe.Pixels()
	if err != nil {
		t.Fatalf("Pixels: %v", err)
	}
	if !sameImage(img, target) {
		t.Fatalf("recipe preview does not reproduce the target pixels")
	}
	pngBytes, err := RenderRecipePNG(result.Recipe)
	if err != nil {
		t.Fatalf("RenderRecipePNG: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("preview PNG does not decode: %v", err)
	}
	if !sameImage(decoded, target) {
		t.Fatalf("preview PNG pixels differ from the target")
	}

	// The result never exceeds the requested resources.
	for _, layer := range result.Recipe {
		if layer.Dye != ColorRed && layer.Dye != ColorYellow {
			t.Fatalf("recipe uses dye %d outside the fixture", layer.Dye)
		}
	}
}

func TestConstrainedThreeDyesFourLayers(t *testing.T) {
	limits := Limits{Dyes: threeDyes, Patterns: fourPatterns(), MaxLayers: 4}
	want := Recipe{
		{Pattern: PatternFull, Dye: ColorYellow},
		{Pattern: PatternUpperHalf, Dye: ColorRed},
		{Pattern: PatternLowerHalf, Dye: ColorBlue},
		{Pattern: PatternCircle, Dye: ColorYellow},
	}
	target := patternTarget(t, want)

	result, err := FindConstrainedRecipe(target, limits, SearchOptions{Seed: 11})
	if err != nil {
		t.Fatalf("FindConstrainedRecipe: %v", err)
	}
	if !result.Found() {
		t.Fatalf("expected an exact recipe, got %s at %.6f", result.Status, result.Fitness)
	}
	recipeFitsLimits(t, result.Recipe, limits)

	img, _ := result.Recipe.Pixels()
	if !sameImage(img, target) {
		t.Fatalf("three-dye recipe does not reproduce the target")
	}
}

// --- requirement 2: deterministic under a fixed seed ----------------------

func TestConstrainedDeterministic(t *testing.T) {
	limits := Limits{Dyes: threeDyes, Patterns: fourPatterns(), MaxLayers: 4}
	target := patternTarget(t, Recipe{
		{Pattern: PatternFull, Dye: ColorBlue},
		{Pattern: PatternUpperHalf, Dye: ColorYellow},
		{Pattern: PatternCircle, Dye: ColorRed},
	})
	opts := SearchOptions{Seed: 12345, ForceGenetic: true}

	first, err := FindConstrainedRecipe(target, limits, opts)
	if err != nil {
		t.Fatalf("first search: %v", err)
	}
	for i := 0; i < 4; i++ {
		again, err := FindConstrainedRecipe(target, limits, opts)
		if err != nil {
			t.Fatalf("repeat %d: %v", i, err)
		}
		if again.Fitness != first.Fitness ||
			again.Comparisons != first.Comparisons ||
			again.Generations != first.Generations ||
			again.Status != first.Status {
			t.Fatalf("repeat %d differs: %+v vs %+v", i, again, first)
		}
		if len(again.Recipe) != len(first.Recipe) {
			t.Fatalf("repeat %d recipe length differs", i)
		}
		for j := range again.Recipe {
			if again.Recipe[j] != first.Recipe[j] {
				t.Fatalf("repeat %d layer %d differs: %v vs %v", i, j, again.Recipe[j], first.Recipe[j])
			}
		}
	}

	// A different seed may (and here does) explore differently, but must
	// still stay inside the limits.
	other, err := FindConstrainedRecipe(target, limits, SearchOptions{Seed: 99, ForceGenetic: true})
	if err != nil {
		t.Fatalf("other seed: %v", err)
	}
	recipeFitsLimits(t, other.Recipe, limits)
}

// TestConstrainedOperatorsRespectLimits drives the mutation and crossover
// operators directly; they are the point where a restriction is easiest to
// lose.
func TestConstrainedOperatorsRespectLimits(t *testing.T) {
	limits := Limits{
		Dyes:      []int{ColorRed, ColorBlue},
		Patterns:  []int{PatternFull, PatternUpperHalf, PatternCircle},
		MaxLayers: 3,
	}
	n, err := limits.normalize()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 500; i++ {
		rec := newRandomConstrainedRecipe(rng, n)
		assertStaysInLimits(t, "initial", rec, limits)
		assertStaysInLimits(t, "mutated", mutateConstrained(rec, rng, n), limits)
		other := newRandomConstrainedRecipe(rng, n)
		assertStaysInLimits(t, "crossover", crossoverConstrained(rec, other, rng, n), limits)
		assertStaysInLimits(t, "sanitized", sanitizeRecipe(crossoverConstrained(rec, other, rng, n), rng, n), limits)
	}

	// A deliberately out-of-limits input must be cleaned up by sanitize.
	bad := Recipe{
		{Pattern: PatternFull, Dye: ColorGreen},
		{Pattern: PatternBorder, Dye: ColorRed},
		{Pattern: PatternFull, Dye: ColorBlue},
	}
	assertStaysInLimits(t, "sanitized-bad", sanitizeRecipe(bad, rng, n), limits)
}

func assertStaysInLimits(t *testing.T, what string, rec Recipe, limits Limits) {
	t.Helper()
	if len(rec) == 0 {
		t.Fatalf("%s produced an empty recipe", what)
	}
	if len(rec) > limits.MaxLayers {
		t.Fatalf("%s produced %d layers, limit %d", what, len(rec), limits.MaxLayers)
	}
	dyes := map[int]bool{}
	for _, d := range limits.Dyes {
		dyes[d] = true
	}
	patterns := map[int]bool{}
	for _, p := range limits.Patterns {
		patterns[p] = true
	}
	seen := map[int]bool{}
	for _, layer := range rec {
		if !dyes[layer.Dye] || !patterns[layer.Pattern] {
			t.Fatalf("%s produced disallowed layer %+v", what, layer)
		}
		if seen[layer.Pattern] {
			t.Fatalf("%s produced duplicate pattern %d", what, layer.Pattern)
		}
		seen[layer.Pattern] = true
	}
}

// The unconstrained search must be unaffected: the package level random
// generators still cover the full ranges.
func TestUnconstrainedSearchUnchanged(t *testing.T) {
	rand.Seed(42)
	sawOutside := false
	outsideLimits := func(p Pattern) bool {
		dyeOutside := p.color != ColorRed && p.color != ColorYellow && p.color != ColorBlue
		patternOutside := p.pattern != PatternFull && p.pattern != PatternUpperHalf &&
			p.pattern != PatternLowerHalf && p.pattern != PatternCircle
		return dyeOutside || patternOutside
	}
	for i := 0; i < 2000; i++ {
		sol := NewRandomSolution()
		for _, p := range sol {
			if outsideLimits(p) {
				sawOutside = true
			}
		}
	}
	if !sawOutside {
		t.Fatalf("unconstrained random solutions never left the constrained fixture; " +
			"the original search may have been accidentally restricted")
	}
	// NewRandomBanner is the original entry point and must still produce a
	// full size banner.
	b, how := NewRandomBanner()
	if len(how) != maxPatterns+1 || len(b.Patterns) != maxPatterns+1 {
		t.Fatalf("NewRandomBanner changed shape: %d patterns", len(how))
	}
}

// --- requirement 3: clear results and search limits -----------------------

func TestConstrainedInputErrors(t *testing.T) {
	target := patternTarget(t, Recipe{{Pattern: PatternFull, Dye: ColorRed}})

	cases := []struct {
		name   string
		limits Limits
		want   error
	}{
		{"empty dyes", Limits{Dyes: nil, Patterns: []int{PatternFull}, MaxLayers: 2}, ErrNoDyes},
		{"empty patterns", Limits{Dyes: []int{ColorRed}, Patterns: nil, MaxLayers: 2}, ErrNoPatterns},
		{"zero layers", Limits{Dyes: []int{ColorRed}, Patterns: []int{PatternFull}, MaxLayers: 0}, ErrZeroLayers},
		{"negative layers", Limits{Dyes: []int{ColorRed}, Patterns: []int{PatternFull}, MaxLayers: -3}, ErrZeroLayers},
		{"too many layers", Limits{Dyes: []int{ColorRed}, Patterns: []int{PatternFull}, MaxLayers: MaxSearchLayers + 1}, ErrLayerLimit},
		{"invalid dye", Limits{Dyes: []int{999}, Patterns: []int{PatternFull}, MaxLayers: 2}, ErrInvalidDye},
		{"invalid pattern", Limits{Dyes: []int{ColorRed}, Patterns: []int{9999}, MaxLayers: 2}, ErrInvalidPattern},
		{"duplicate pattern", Limits{Dyes: []int{ColorRed}, Patterns: []int{PatternFull, PatternFull}, MaxLayers: 2}, ErrDuplicatePattern},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FindConstrainedRecipe(target, tc.limits, SearchOptions{Seed: 1})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}

	if _, err := FindConstrainedRecipe(nil, Limits{Dyes: []int{ColorRed}, Patterns: []int{PatternFull}, MaxLayers: 1}, SearchOptions{}); !errors.Is(err, ErrNilTarget) {
		t.Fatalf("nil target: got %v, want ErrNilTarget", err)
	}
	small := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	if _, err := FindConstrainedRecipe(small, Limits{Dyes: []int{ColorRed}, Patterns: []int{PatternFull}, MaxLayers: 1}, SearchOptions{}); !errors.Is(err, ErrTargetSize) {
		t.Fatalf("small target: got %v, want ErrTargetSize", err)
	}
}

func TestConstrainedUndrawableTarget(t *testing.T) {
	// A solid green target can not be produced from red and yellow dyes.
	target := image.NewNRGBA(image.Rect(0, 0, BannerW, BannerH))
	for y := 0; y < BannerH; y++ {
		for x := 0; x < BannerW; x++ {
			target.SetNRGBA(x, y, color.NRGBA{R: 0, G: 255, B: 0, A: 255})
		}
	}
	limits := Limits{Dyes: []int{ColorRed, ColorYellow}, Patterns: []int{PatternFull}, MaxLayers: 2}
	_, err := FindConstrainedRecipe(target, limits, SearchOptions{Seed: 1})
	if !errors.Is(err, ErrUndrawableTarget) {
		t.Fatalf("got %v, want ErrUndrawableTarget", err)
	}
}

func TestConstrainedZeroOrDuplicateRecipe(t *testing.T) {
	target := patternTarget(t, Recipe{{Pattern: PatternFull, Dye: ColorRed}})

	// A zero length recipe is explicitly invalid.
	if err := (Recipe{}).Validate(); !errors.Is(err, ErrZeroLayers) {
		t.Fatalf("empty recipe: got %v, want ErrZeroLayers", err)
	}
	dup := Recipe{
		{Pattern: PatternFull, Dye: ColorRed},
		{Pattern: PatternFull, Dye: ColorBlue},
	}
	if err := dup.Validate(); !errors.Is(err, ErrDuplicatePattern) {
		t.Fatalf("duplicate recipe: got %v, want ErrDuplicatePattern", err)
	}

	// Duplicate allowed patterns are a configuration error, mirrored by the
	// search entry point.
	limits := Limits{Dyes: []int{ColorRed}, Patterns: []int{PatternFull, PatternUpperHalf, PatternFull}, MaxLayers: 2}
	if _, err := FindConstrainedRecipe(target, limits, SearchOptions{Seed: 1}); !errors.Is(err, ErrDuplicatePattern) {
		t.Fatalf("got %v, want ErrDuplicatePattern", err)
	}
	// Duplicate dyes are harmless and collapse.
	ok, err := FindConstrainedRecipe(target, Limits{
		Dyes: []int{ColorRed, ColorRed}, Patterns: []int{PatternFull}, MaxLayers: 1,
	}, SearchOptions{Seed: 1})
	if err != nil || !ok.Found() {
		t.Fatalf("duplicate dyes should be accepted: %v, %+v", err, ok)
	}
}

func TestConstrainedBudgetExhausted(t *testing.T) {
	// A target that needs the full 4-layer space, with a budget too small to
	// enumerate it, must stop cleanly at the cap and report why.
	limits := Limits{Dyes: threeDyes, Patterns: fourPatterns(), MaxLayers: 4}
	target := patternTarget(t, Recipe{
		{Pattern: PatternFull, Dye: ColorYellow},
		{Pattern: PatternUpperHalf, Dye: ColorRed},
		{Pattern: PatternLowerHalf, Dye: ColorBlue},
		{Pattern: PatternCircle, Dye: ColorRed},
	})
	const budget = 25
	result, err := FindConstrainedRecipe(target, limits, SearchOptions{
		Seed: 5, MaxEvaluations: budget, ForceGenetic: true,
	})
	if err != nil {
		t.Fatalf("budget search returned an error: %v", err)
	}
	if result.Status != StatusBudgetExhausted {
		t.Fatalf("status = %s, want %s", result.Status, StatusBudgetExhausted)
	}
	if result.Comparisons > budget {
		t.Fatalf("used %d comparisons, budget was %d", result.Comparisons, budget)
	}
	if result.Fitness < 0 || result.Fitness > 1 {
		t.Fatalf("fitness out of range: %v", result.Fitness)
	}
	if len(result.Recipe) == 0 {
		t.Fatalf("budget-exhausted search should still return its best recipe")
	}
	recipeFitsLimits(t, result.Recipe, limits)

	// The exhaustive path also respects the budget: the same small space is
	// enumerated, but it can not finish within 25 evaluations.
	exhaustive, err := FindConstrainedRecipe(target, limits, SearchOptions{Seed: 5, MaxEvaluations: budget})
	if err != nil {
		t.Fatalf("exhaustive budget search: %v", err)
	}
	if !exhaustive.Exhaustive {
		t.Fatalf("expected the exhaustive path for this small space")
	}
	if exhaustive.Status != StatusBudgetExhausted {
		t.Fatalf("exhaustive status = %s, want budget exhausted", exhaustive.Status)
	}
	if exhaustive.Comparisons > budget {
		t.Fatalf("exhaustive used %d comparisons, budget was %d", exhaustive.Comparisons, budget)
	}
	if len(exhaustive.Recipe) == 0 {
		t.Fatalf("exhaustive budget search should still return its best recipe")
	}
	recipeFitsLimits(t, exhaustive.Recipe, limits)
}

func TestConstrainedOptionsAreClamped(t *testing.T) {
	target := patternTarget(t, Recipe{{Pattern: PatternFull, Dye: ColorRed}})
	limits := Limits{Dyes: []int{ColorRed}, Patterns: []int{PatternFull}, MaxLayers: 1}
	result, err := FindConstrainedRecipe(target, limits, SearchOptions{
		Seed:           1,
		PopulationSize: HardMaxPopulationSize + 100,
		Generations:    HardMaxGenerations + 100,
		MaxEvaluations: HardMaxEvaluations + 100,
		ForceGenetic:   true,
	})
	if err != nil {
		t.Fatalf("clamped search: %v", err)
	}
	if !result.Clamped {
		t.Fatalf("expected Clamped to be reported")
	}
	if result.PopulationSize != HardMaxPopulationSize {
		t.Fatalf("population = %d, want %d", result.PopulationSize, HardMaxPopulationSize)
	}
	if result.MaxGenerations != HardMaxGenerations {
		t.Fatalf("generations = %d, want %d", result.MaxGenerations, HardMaxGenerations)
	}
	if result.MaxEvaluations != HardMaxEvaluations {
		t.Fatalf("evaluations = %d, want %d", result.MaxEvaluations, HardMaxEvaluations)
	}
}

func TestConstrainedBestEffort(t *testing.T) {
	// A target reachable only in a large space, with a tiny generation count,
	// must return a best-effort recipe rather than an error.
	limits := Limits{Dyes: threeDyes, Patterns: fourPatterns(), MaxLayers: 4}
	target := patternTarget(t, Recipe{
		{Pattern: PatternFull, Dye: ColorYellow},
		{Pattern: PatternUpperHalf, Dye: ColorRed},
		{Pattern: PatternCircle, Dye: ColorBlue},
		{Pattern: PatternLowerHalf, Dye: ColorRed},
	})
	result, err := FindConstrainedRecipe(target, limits, SearchOptions{
		Seed: 2, PopulationSize: 6, Generations: 1, ForceGenetic: true,
	})
	if err != nil {
		t.Fatalf("best-effort search: %v", err)
	}
	if result.Status != StatusBestEffort && result.Status != StatusFound {
		t.Fatalf("unexpected status %s", result.Status)
	}
	recipeFitsLimits(t, result.Recipe, limits)
	if result.Fitness <= 0 {
		t.Fatalf("best effort should still score above zero, got %v", result.Fitness)
	}
}

func TestConstrainedExhaustiveFindsShortest(t *testing.T) {
	// With an exhaustive (small) space, the shortest exact recipe wins.
	limits := Limits{
		Dyes:      []int{ColorRed, ColorBlue},
		Patterns:  []int{PatternFull, PatternUpperHalf, PatternLowerHalf},
		MaxLayers: 3,
	}
	want := Recipe{
		{Pattern: PatternFull, Dye: ColorBlue},
		{Pattern: PatternUpperHalf, Dye: ColorRed},
	}
	target := patternTarget(t, want)
	result, err := FindConstrainedRecipe(target, limits, SearchOptions{Seed: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !result.Exhaustive {
		t.Fatalf("expected the exhaustive path for a tiny space")
	}
	if !result.Found() {
		t.Fatalf("expected exact result, got %s", result.Status)
	}
	if len(result.Recipe) != 2 {
		t.Fatalf("expected the shortest 2-layer recipe, got %d layers: %v", len(result.Recipe), result.Recipe)
	}
	recipeFitsLimits(t, result.Recipe, limits)
	img, _ := result.Recipe.Pixels()
	if !sameImage(img, target) {
		t.Fatalf("exhaustive recipe does not reproduce pixels")
	}
}

func TestConstrainedSVGPreview(t *testing.T) {
	rec := Recipe{
		{Pattern: PatternFull, Dye: ColorYellow},
		{Pattern: PatternUpperHalf, Dye: ColorRed},
	}
	svg := rec.SVG()
	if !bytes.Contains(svg, []byte("<svg")) {
		t.Fatalf("SVG preview is missing the svg tag: %s", svg)
	}
	pngBytes := rec.PNG()
	if _, err := png.Decode(bytes.NewReader(pngBytes)); err != nil {
		t.Fatalf("PNG preview does not decode: %v", err)
	}
}

// Antialiased pixels that are blends of allowed dyes must not be mistaken for
// an undrawable target.
func TestConstrainedAntialiasedTargetAccepted(t *testing.T) {
	limits := Limits{
		Dyes:      []int{ColorBlue, ColorRed, ColorYellow, ColorWhite},
		Patterns:  []int{PatternFull, PatternDiagonal1, PatternCircle, PatternDiamond},
		MaxLayers: 4,
	}
	// A blue background, red diagonal, yellow roundel and white lozenge
	// produce many three-color blend pixels at the overlaps.
	target := patternTarget(t, Recipe{
		{Pattern: PatternFull, Dye: ColorBlue},
		{Pattern: PatternDiagonal1, Dye: ColorRed},
		{Pattern: PatternCircle, Dye: ColorYellow},
		{Pattern: PatternDiamond, Dye: ColorWhite},
	})
	result, err := FindConstrainedRecipe(target, limits, SearchOptions{Seed: 3})
	if err != nil {
		t.Fatalf("antialiased target rejected: %v", err)
	}
	if !result.Found() {
		t.Fatalf("expected an exact recipe, got %s at %.6f", result.Status, result.Fitness)
	}
	recipeFitsLimits(t, result.Recipe, limits)
	img, _ := result.Recipe.Pixels()
	if !sameImage(img, target) {
		t.Fatalf("recipe does not reproduce the antialiased target")
	}
}

// A gray blend of white and a dye is reachable in principle, so it must not be
// reported as undrawable, even when the allowed layer count is too small to
// paint it exactly.
func TestConstrainedBlendIsBestEffortNotUndrawable(t *testing.T) {
	target := image.NewNRGBA(image.Rect(0, 0, BannerW, BannerH))
	for y := 0; y < BannerH; y++ {
		for x := 0; x < BannerW; x++ {
			target.SetNRGBA(x, y, color.NRGBA{R: 128, G: 128, B: 128, A: 255})
		}
	}
	result, err := FindConstrainedRecipe(target, Limits{
		Dyes:      []int{ColorBlack},
		Patterns:  []int{PatternFull},
		MaxLayers: 1,
	}, SearchOptions{Seed: 1})
	if err != nil {
		t.Fatalf("blend should not be undrawable: %v", err)
	}
	if result.Found() {
		t.Fatalf("a single black layer can not paint gray exactly")
	}
	if result.Fitness <= 0 {
		t.Fatalf("expected a positive best-effort fitness, got %v", result.Fitness)
	}
}

// Partial transparency is flattened over white, matching the old
// "rsvg-convert -b white" behavior.
func TestTransparentTargetOverWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, BannerW, BannerH))
	for y := 0; y < BannerH; y++ {
		for x := 0; x < BannerW; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 0})
		}
	}
	white := solidDyeColor(ColorBrightWhite)
	converted := opaqueOverWhite(color.NRGBA{A: 0})
	if converted != white {
		t.Fatalf("transparent pixel became %v, want white %v", converted, white)
	}
	// An all-transparent target is equivalent to a white target, which the
	// search must accept.
	result, err := FindConstrainedRecipe(img, Limits{
		Dyes: []int{ColorRed}, Patterns: []int{PatternFull}, MaxLayers: 1,
	}, SearchOptions{Seed: 1})
	if err != nil {
		t.Fatalf("transparent target rejected: %v", err)
	}
	if result.Fitness < 0 {
		t.Fatalf("unexpected fitness %v", result.Fitness)
	}
}

func TestConstrainedErrorsUnwrap(t *testing.T) {
	_, err := FindConstrainedRecipe(image.NewNRGBA(image.Rect(0, 0, BannerW, BannerH)),
		Limits{Dyes: []int{ColorRed}, Patterns: []int{PatternFull}, MaxLayers: 9},
		SearchOptions{})
	if !errors.Is(err, ErrLayerLimit) {
		t.Fatalf("got %v, want an error wrapping ErrLayerLimit", err)
	}
	if !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("error message lost its detail: %v", err)
	}
}
