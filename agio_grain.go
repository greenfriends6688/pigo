package main

// The film grain over the window's chrome, copied from Agio.

import "github.com/oneo-me/nsgo/core"

var grainProgram = core.MustCompileShader(`package main

var Amount float

func hash(p vec2) float {
	p3 := fract(vec3(p.x, p.y, p.x) * 0.1031)
	p3 += dot(p3, p3.yzx+33.33)
	return fract((p3.x + p3.y) * p3.z)
}

func Fragment(dst vec4, pos vec2) vec4 {
	n := hash(floor(dst.xy))
	a := Amount * abs(n-0.5) * 2
	if n > 0.5 {
		return vec4(a, a, a, a)
	}
	return vec4(0, 0, 0, a)
}
`)

const (
	defaultGrain = 40
	grainOpacity = 0.16
)

func grainAmount(v int) int {
	if v <= 0 {
		return defaultGrain
	}
	return min(v, 100)
}

// grainBackdrop is the window's grain while the setting turns it on, for
// ui.SidebarShell's Backdrop.
func (s *appState) grainBackdrop() []core.Node {
	return []core.Node{core.Show{When: s.grainOn.Get, Then: func(cx *core.Scope) core.Node {
		return core.Shader{Program: grainProgram, TestID: "grain",
			Style: core.Style{Width: core.Pct(100), Height: core.Pct(100)},
			UniformsFunc: func() core.Uniforms {
				return core.Uniforms{"Amount": float64(grainAmount(s.grainAmount.Get())) / 100 * grainOpacity}
			}}
	}}}
}
