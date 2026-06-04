package slider

import (
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"sort"
	"time"
)

// GapResult represents a detected gap position with confidence
type GapResult struct {
	X         int
	Score     float64
	Candidates []GapCandidate
}

// GapCandidate represents a candidate gap position
type GapCandidate struct {
	X     int
	Score float64
}

// DetectGap detects the slider gap position using edge detection
// This is the Go equivalent of Python's _ysb_identify_gap method
// which uses cv2 template matching on alpha channels
//
// Strategy:
// 1. Download bg and puzzle images
// 2. Convert to grayscale
// 3. Apply edge detection (Sobel filter)
// 4. Template matching or feature-based gap detection
// 5. Return gap x-position with confidence
func DetectGap(bgURL, tpURL string) (*GapResult, error) {
	// Download images
	bgImg, err := downloadImage(bgURL)
	if err != nil {
		return nil, fmt.Errorf("download bg image: %w", err)
	}
	tpImg, err := downloadImage(tpURL)
	if err != nil {
		return nil, fmt.Errorf("download tp image: %w", err)
	}

	return detectGapFromImages(bgImg, tpImg)
}

func downloadImage(url string) (image.Image, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("image decode: %w", err)
	}
	return img, nil
}

func detectGapFromImages(bg, tp image.Image) (*GapResult, error) {
	bgBounds := bg.Bounds()
	tpBounds := tp.Bounds()

	bgGray := toGray(bg)
	tpGray := toGray(tp)

	// Apply edge detection
	bgEdges := sobelEdge(bgGray, bgBounds)
	tpEdges := sobelEdge(tpGray, tpBounds)

	// Template matching: slide puzzle across background and compute match score
	tpW := tpBounds.Dx()
	tpH := tpBounds.Dy()
	bgW := bgBounds.Dx()
	bgH := bgBounds.Dy()

	var candidates []GapCandidate

	// Only check the relevant portion (gap is never at the very left or very right)
	startX := 40 // skip left edge
	endX := bgW - tpW - 10

	for x := startX; x < endX; x += 2 { // step=2 for speed
		score := matchScore(bgEdges, tpEdges, x, 0, bgW, bgH, tpW, tpH)
		candidates = append(candidates, GapCandidate{X: x, Score: score})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no gap candidates found")
	}

	// Sort by score (higher = better match)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	// Filter top candidates (score > 0.5 * best)
	bestScore := candidates[0].Score
	var topCandidates []GapCandidate
	for _, c := range candidates {
		if c.Score >= bestScore*0.5 {
			topCandidates = append(topCandidates, c)
		}
	}

	// Return result with all top candidates for multi-retry
	rawDistance := candidates[0].X

	return &GapResult{
		X:     rawDistance,
		Score: candidates[0].Score,
		Candidates: topCandidates,
	}, nil
}

// toGray converts image to grayscale
func toGray(img image.Image) [][]float64 {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	gray := make([][]float64, h)
	for y := 0; y < h; y++ {
		gray[y] = make([]float64, w)
		for x := 0; x < w; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			// Luminance formula
			gray[y][x] = 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
		}
	}
	return gray
}

// sobelEdge applies Sobel edge detection
func sobelEdge(gray [][]float64, bounds image.Rectangle) [][]float64 {
	w, h := bounds.Dx(), bounds.Dy()
	edges := make([][]float64, h)
	for y := 0; y < h; y++ {
		edges[y] = make([]float64, w)
	}

	// Sobel kernels
	gx := [3][3]float64{{-1, 0, 1}, {-2, 0, 2}, {-1, 0, 1}}
	gy := [3][3]float64{{-1, -2, -1}, {0, 0, 0}, {1, 2, 1}}

	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			var sx, sy float64
			for ky := -1; ky <= 1; ky++ {
				for kx := -1; kx <= 1; kx++ {
					val := gray[y+ky][x+kx]
					sx += val * gx[ky+1][kx+1]
					sy += val * gy[ky+1][kx+1]
				}
			}
			edges[y][x] = math.Sqrt(sx*sx + sy*sy)
		}
	}

	return edges
}

// matchScore computes normalized cross-correlation between template and background region
func matchScore(bgEdges, tpEdges [][]float64, offsetX, offsetY, bgW, bgH, tpW, tpH int) float64 {
	if offsetX+tpW > bgW || offsetY+tpH > bgH {
		return 0
	}

	var sumBgTp, sumBg2, sumTp2 float64
	var count int

	for y := 0; y < tpH; y++ {
		for x := 0; x < tpW; x++ {
			bgVal := bgEdges[offsetY+y][offsetX+x]
			tpVal := tpEdges[y][x]

			// Only consider edge pixels (non-zero in template)
			if tpVal > 10 {
				sumBgTp += bgVal * tpVal
				sumBg2 += bgVal * bgVal
				sumTp2 += tpVal * tpVal
				count++
			}
		}
	}

	if count == 0 || sumBg2 == 0 || sumTp2 == 0 {
		return 0
	}

	// Normalized cross-correlation
	return sumBgTp / (math.Sqrt(sumBg2) * math.Sqrt(sumTp2))
}

// DetectGapFromReaders detects gap from io.Reader sources (for testing)
func DetectGapFromReaders(bgReader, tpReader io.Reader) (*GapResult, error) {
	bgImg, _, err := image.Decode(bgReader)
	if err != nil {
		return nil, fmt.Errorf("decode bg: %w", err)
	}
	tpImg, _, err := image.Decode(tpReader)
	if err != nil {
		return nil, fmt.Errorf("decode tp: %w", err)
	}
	return detectGapFromImages(bgImg, tpImg)
}

// Unused import suppression
var _ color.Color
