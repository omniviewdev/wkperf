package flame

import (
	"fmt"
	"math"

	core "github.com/omniviewdev/wkperf/internal/flame"
)

const diffPaletteSteps = 100

type framePaint struct {
	Background string `json:"background"`
	Foreground string `json:"foreground"`
}

// The browser consumes this exact opaque palette instead of implementing its own
// interpolation or contrast calculation. SVG and static HTML use it directly.
var diffPalette = makeDiffPalette()

func makeDiffPalette() [2*diffPaletteSteps + 1]framePaint {
	var palette [2*diffPaletteSteps + 1]framePaint
	neutral := [3]int{113, 128, 150}
	for i := range palette {
		end := [3]int{220, 60, 65}
		if i < diffPaletteSteps {
			end = [3]int{35, 150, 105}
		}
		share := math.Abs(float64(i-diffPaletteSteps)) / diffPaletteSteps
		var rgb [3]int
		for channel := range rgb {
			rgb[channel] = int(math.Round(float64(neutral[channel]) + share*float64(end[channel]-neutral[channel])))
		}
		background := fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2])
		luminance := 0.0
		for channel, coefficient := range [3]float64{.2126, .7152, .0722} {
			value := float64(rgb[channel]) / 255
			if value <= .04045 {
				value /= 12.92
			} else {
				value = math.Pow((value+.055)/1.055, 2.4)
			}
			luminance += coefficient * value
		}
		foreground := "#000000"
		if 1.05/(luminance+.05) > (luminance+.05)/.05 {
			foreground = "#ffffff"
		}
		palette[i] = framePaint{background, foreground}
	}
	return palette
}

func maxAbsDelta(root *viewNode) float64 {
	maximum := math.Abs(root.DeltaMS)
	for _, child := range root.Children {
		maximum = math.Max(maximum, maxAbsDelta(child))
	}
	return maximum
}

func nodePaint(node *viewNode, diff bool, maximum float64) framePaint {
	if diff {
		share := 0.0
		if maximum > 0 {
			share = math.Max(-1, math.Min(1, node.DeltaMS/maximum))
		}
		return diffPalette[int(math.Round(float64(diffPaletteSteps)*(1+share)))]
	}
	background := "#718096"
	switch node.Category {
	case core.CategoryApp:
		background = "#60a5fa"
	case core.CategoryFramework:
		background = "#a78bfa"
	case core.CategoryNative:
		background = "#94a3b8"
	case core.CategoryGC:
		background = "#fbbf24"
	}
	return framePaint{background, "#000000"}
}
