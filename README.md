# mcbanner

Application for generating Minecraft banners.

## Includes

* A web server/page showing the variations of Minecraft banners, as SVG approximations (`mcweb`).
* A commandline application for evolving Minecraft banners with genetic algorithms (`evolve`).
* A commandline application for searching a banner recipe with a limited set of dyes, patterns and layers (`evolve-constrained`).
* Go code for generating a random SVG banner and rendering it as PNG by using `rsvg-convert` (`random`).

## Constrained recipe search

`FindConstrainedRecipe` searches only the dyes, patterns and layers that are
actually available, and returns a per-layer recipe that can be recreated in
game. It is deterministic for a given seed.

```go
limits := mcbanner.Limits{
    Dyes:      []int{mcbanner.ColorRed, mcbanner.ColorYellow, mcbanner.ColorBlue},
    Patterns:  []int{mcbanner.PatternFull, mcbanner.PatternUpperHalf, mcbanner.PatternCircle},
    MaxLayers: 4, // 1..6, and never more than len(Patterns)
}
result, err := mcbanner.FindConstrainedRecipe(targetImage, limits, mcbanner.SearchOptions{Seed: 42})
if err != nil {
    // ErrNoDyes, ErrNoPatterns, ErrZeroLayers, ErrLayerLimit,
    // ErrDuplicatePattern, ErrInvalidDye/Pattern, ErrTargetSize or
    // ErrUndrawableTarget
}
fmt.Println(result)              // status, fitness and the recipe
png := result.PreviewPNG()       // built-in renderer, no rsvg-convert needed
svg := result.PreviewSVG()

```

Notes:

* `StatusFound` means an exact pixel match, `StatusBestEffort` means the
  generation limit was reached and `StatusBudgetExhausted` means the
  `MaxEvaluations` cap was reached. The last two still return the best recipe.
* `Limits.Patterns` must not contain duplicates, and a recipe never applies the
  same pattern twice.
* Large option values are clamped to the hard caps reported in
  `SearchResult.PopulationSize`, `.MaxGenerations` and `.MaxEvaluations`.
* Small search spaces are enumerated exhaustively, which finds the shortest
  exact recipe. The genetic path can be forced with `SearchOptions.ForceGenetic`.
* The unconstrained genetic search (`FindBest`) is unchanged.

The commandline tool follows the same API:

```sh
go run ./cmd/evolve-constrained -list
go run ./cmd/evolve-constrained \
  -target cmd/mcweb/public/img/c2.png \
  -dyes red,yellow,blue -patterns full,upper-half,roundel -layers 4 \
  -seed 42 -out /tmp/best
```


## Original goal

* Given an image, get the steps for creating the closest looking Minecraft banner. This goal is a work in progress, since the evolution is not rapid enough. Perhaps using smaller images when rendering from SVG, or another algorithm, would work.

## General information

* Version: 2.0.1
* License: MIT
