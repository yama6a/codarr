package ffprobe

import "strings"

// Chroma is a pixel format's chroma subsampling. plan.md 6.2 makes the copy
// test a subsampling test: yuv420p, yuv420p10le and p010le are all 4:2:0, and
// a string compare against "yuv420p" fails every HEVC Main 10 file.
type Chroma string

// The subsamplings ffprobe pixel formats reduce to.
const (
	Chroma420     Chroma = "4:2:0"
	Chroma422     Chroma = "4:2:2"
	Chroma444     Chroma = "4:4:4"
	ChromaSub     Chroma = "subsampled" // 4:1:1, 4:1:0, 4:4:0 and friends
	ChromaMono    Chroma = "monochrome"
	ChromaUnknown Chroma = "unknown"
)

// ChromaOf classifies an ffprobe pix_fmt.
func ChromaOf(pixFmt string) Chroma {
	f := strings.ToLower(pixFmt)

	if c, ok := namedChroma(f); ok {
		return c
	}

	if isMono(f) {
		return ChromaMono
	}

	if c, ok := tokenChroma(f); ok {
		return c
	}

	if isRGB(f) {
		return Chroma444
	}

	return ChromaUnknown
}

func isMono(f string) bool {
	return strings.HasPrefix(f, "gray") || strings.HasPrefix(f, "ya") || f == "monow" || f == "monob"
}

// tokenChroma reads the subsampling out of the name, which is where every
// planar format carries it.
func tokenChroma(f string) (Chroma, bool) {
	switch {
	case strings.Contains(f, "444"):
		return Chroma444, true
	case strings.Contains(f, "422"):
		return Chroma422, true
	case strings.Contains(f, "420"):
		return Chroma420, true
	case strings.Contains(f, "411"), strings.Contains(f, "410"), strings.Contains(f, "440"):
		return ChromaSub, true
	default:
		return ChromaUnknown, false
	}
}

// namedChroma covers the packed and semi-planar formats whose names carry no
// subsampling token.
func namedChroma(f string) (Chroma, bool) {
	switch f {
	case "":
		return ChromaUnknown, true
	case "nv12", "nv21", "p010le", "p010be", "p012le", "p012be", "p016le", "p016be":
		return Chroma420, true
	case "nv16", "nv20le", "nv20be", "p210le", "p210be", "p216le", "p216be",
		"yuyv422", "yvyu422", "uyvy422", "nv24", "nv42":
		return Chroma422, true
	case "p410le", "p410be", "p416le", "p416be", "ayuv64le", "ayuv64be", "xyz12le", "xyz12be":
		return Chroma444, true
	default:
		return ChromaUnknown, false
	}
}

func isRGB(f string) bool {
	for _, p := range []string{"rgb", "bgr", "gbr", "argb", "abgr", "rgba", "bgra", "pal8"} {
		if strings.HasPrefix(f, p) {
			return true
		}
	}

	return false
}

// bitDepthOf reads bits per component out of a pix_fmt name. 8 is the answer
// for anything that does not say otherwise, including unknown formats.
func bitDepthOf(pixFmt string) int {
	f := strings.ToLower(pixFmt)
	if f == "" {
		return 0
	}

	switch f {
	case "p010le", "p010be", "p210le", "p210be", "p410le", "p410be":
		return 10
	case "p012le", "p012be":
		return 12
	case "p016le", "p016be", "p216le", "p216be", "p416le", "p416be", "ayuv64le", "ayuv64be":
		return 16
	case "nv20le", "nv20be":
		return 10
	}

	f = strings.TrimSuffix(strings.TrimSuffix(f, "le"), "be")

	// The depth is the digit run after the subsampling token, so yuv420p10
	// reads 10 while rgb24 has no token and stays 8.
	for _, tok := range []string{"420", "422", "444", "411", "410", "440"} {
		i := strings.Index(f, tok)
		if i < 0 {
			continue
		}

		return depthSuffix(f[i+len(tok):])
	}

	if strings.HasPrefix(f, "gray") {
		return depthSuffix(strings.TrimPrefix(f, "gray"))
	}

	// Planar RGB carries its depth the same way but has no subsampling token.
	if i := strings.LastIndexByte(f, 'p'); i >= 0 {
		return depthSuffix(f[i+1:])
	}

	return 8
}

func depthSuffix(s string) int {
	s = strings.TrimPrefix(s, "p")
	s = strings.TrimPrefix(s, "a")

	depth := 0

	for _, r := range s {
		if r < '0' || r > '9' {
			return 8
		}

		depth = depth*10 + int(r-'0')
	}

	if depth == 0 {
		return 8
	}

	return depth
}
