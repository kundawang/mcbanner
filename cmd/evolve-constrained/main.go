package main

// evolve-constrained searches for a banner recipe that only uses a limited
// set of dyes and patterns and at most a given number of layers.
//
// Example:
//
//	go run ./cmd/evolve-constrained \
//	  -target ../../cmd/mcweb/public/img/c2.png \
//	  -dyes red,yellow,blue -patterns full,upper-half,lower-half,circle \
//	  -layers 4 -seed 42 -out /tmp/best
//
// The per-layer recipe is printed to stdout and a preview is written as
// <out>.png and <out>.svg. The search itself is in the mcbanner package.

import (
	"flag"
	"fmt"
	"image/png"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/xyproto/mcbanner"
)

var (
	targetFile = flag.String("target", "", "target PNG image (20x40)")
	dyesFlag   = flag.String("dyes", "red,yellow,blue", "comma separated allowed dyes")
	patFlag    = flag.String("patterns", "full", "comma separated allowed patterns")
	layers     = flag.Int("layers", 4, "maximum number of layers (1..6)")
	seed       = flag.Int64("seed", 1, "random seed")
	popSize    = flag.Int("population", 0, "population size (0 uses the default)")
	gens       = flag.Int("generations", 0, "maximum generations (0 uses the default)")
	budget     = flag.Int("evaluations", 0, "fitness evaluation budget (0 uses the default)")
	genetic    = flag.Bool("genetic", false, "always use the genetic algorithm, even for small spaces")
	outPrefix  = flag.String("out", "", "write <out>.png and <out>.svg previews")
	showLimits = flag.Bool("list", false, "list dye and pattern names and exit")
)

var dyeByName = map[string]int{
	"white": mcbanner.ColorWhite, "orange": mcbanner.ColorOrange,
	"magenta": mcbanner.ColorMagenta, "lightblue": mcbanner.ColorLightBlue,
	"yellow": mcbanner.ColorYellow, "lime": mcbanner.ColorLime,
	"pink": mcbanner.ColorPink, "gray": mcbanner.ColorGray,
	"lightgray": mcbanner.ColorLightGray, "cyan": mcbanner.ColorCyan,
	"purple": mcbanner.ColorPurple, "blue": mcbanner.ColorBlue,
	"brown": mcbanner.ColorBrown, "green": mcbanner.ColorGreen,
	"red": mcbanner.ColorRed, "black": mcbanner.ColorBlack,
}

var patternByName = map[string]int{
	"base-fess": mcbanner.PatternLowerThird, "chief-fess": mcbanner.PatternUpperThird,
	"pale-dexter": mcbanner.PatternLeftThird, "pale-sinister": mcbanner.PatternRightThird,
	"pale": mcbanner.PatternCenterThird, "fess": mcbanner.PatternHorizontalLine,
	"bend": mcbanner.PatternDiagonal1, "bend-sinister": mcbanner.PatternDiagonal2,
	// Paly sits directly before the saltire in the pattern constants.
	"paly": mcbanner.PatternDiaCross - 1, "saltire": mcbanner.PatternDiaCross,
	"cross":                      mcbanner.PatternCross,
	"per-bend-sinister":          mcbanner.PatternUpperLeftTriangle,
	"per-bend":                   mcbanner.PatternUpperRightTriangle,
	"per-bend-inverted":          mcbanner.PatternLowerLeftTriangle,
	"per-bend-sinister-inverted": mcbanner.PatternLowerRightTriangle,
	"per-pale":                   mcbanner.PatternLeftHalf, "per-pale-inverted": mcbanner.PatternRightHalf,
	"per-fess": mcbanner.PatternUpperHalf, "per-fess-inverted": mcbanner.PatternLowerHalf,
	"upper-half": mcbanner.PatternUpperHalf, "lower-half": mcbanner.PatternLowerHalf,
	"base-dexter-canton":    mcbanner.PatternLowerLeftSquare,
	"base-sinister-canton":  mcbanner.PatternLowerRightSquare,
	"chief-dexter-canton":   mcbanner.PatternUpperLeftSquare,
	"chief-sinister-canton": mcbanner.PatternUpperRightSqaure,
	"chevron":               mcbanner.PatternLowerTriangle, "inverted-chevron": mcbanner.PatternUpperTriangle,
	"base-indented": mcbanner.PatternLowerWaves, "chief-indented": mcbanner.PatternUpperWaves,
	"roundel": mcbanner.PatternCircle, "lozenge": mcbanner.PatternDiamond,
	"bordure": mcbanner.PatternBorder, "bordure-indented": mcbanner.PatternWaveBorder,
	"field-masoned": mcbanner.PatternBricks,
	"gradient":      mcbanner.PatternGradientDown, "base-gradient": mcbanner.PatternGradientUp,
	"creeper": mcbanner.PatternCreeper, "skull": mcbanner.PatternSkull,
	"flower": mcbanner.PatternFlower, "mojang": mcbanner.PatternLogo,
	"full": mcbanner.PatternFull,
}

func parseList(commaSeparated string, lookup map[string]int, kind string) []int {
	var values []int
	for _, part := range strings.Split(commaSeparated, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		id, ok := lookup[name]
		if !ok {
			log.Fatalf("unknown %s %q", kind, name)
		}
		values = append(values, id)
	}
	return values
}

func main() {
	flag.Parse()

	if *showLimits {
		for _, name := range sortedKeys(dyeByName) {
			fmt.Printf("dye     %-12s %d\n", name, dyeByName[name])
		}
		for _, name := range sortedKeys(patternByName) {
			fmt.Printf("pattern %-28s %d\n", name, patternByName[name])
		}
		return
	}

	if *targetFile == "" {
		log.Fatal("missing -target")
	}
	f, err := os.Open(*targetFile)
	if err != nil {
		log.Fatalf("could not open target: %v", err)
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		log.Fatalf("could not decode target: %v", err)
	}

	limits := mcbanner.Limits{
		Dyes:      parseList(*dyesFlag, dyeByName, "dye"),
		Patterns:  parseList(*patFlag, patternByName, "pattern"),
		MaxLayers: *layers,
	}
	opts := mcbanner.SearchOptions{
		Seed:           *seed,
		PopulationSize: *popSize,
		Generations:    *gens,
		MaxEvaluations: *budget,
		ForceGenetic:   *genetic,
	}

	result, err := mcbanner.FindConstrainedRecipe(img, limits, opts)
	if err != nil {
		log.Fatalf("search failed: %v", err)
	}

	fmt.Println(result)
	if result.Found() {
		fmt.Println("exact match found")
	}
	if *outPrefix != "" {
		if err := ioutil.WriteFile(*outPrefix+".png", result.PreviewPNG(), 0644); err != nil {
			log.Fatalf("could not write preview PNG: %v", err)
		}
		if err := ioutil.WriteFile(*outPrefix+".svg", result.PreviewSVG(), 0644); err != nil {
			log.Fatalf("could not write preview SVG: %v", err)
		}
	}

}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
