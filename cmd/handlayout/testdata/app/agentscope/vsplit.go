// Package agentscope holds minimal excerpts of github.com/go-agentsync/agentscope
// at tag v0.10.4 (ui/vsplit.go, ui/scene.go, ui/scene_guard.go). The statements
// are verbatim; the surrounding types are the least that lets them type-check.
// The line numbers of the four sites are pinned by the test.
package ui

import (
	"github.com/go-widgets/toolkit"
)

const (
	SplitScale = 1000
	PaneMinH   = 120
	cardPad    = 12
	gap        = 9
	margin     = 8
	headerH    = 40
	controlsH  = 24
	minTableH  = 60

	minTableFloor  = 36
	minDetailChars = 20
)

type obs struct{ v int }

func (o *obs) Get() int  { return o.v }
func (o *obs) Set(v int) { o.v = v }

type ViewModel struct {
	Split, DetailChars obs
	Guard              struct{ Chars obs }
}

func (v *ViewModel) EffectivePanelW() int { return 360 }

type Scene struct {
	vm           *ViewModel
	w, h         int
	filtersShown bool
}

func (s *Scene) px(n int) int { return n }

// sessionsShare is the height the Sessions card gets out of room (vsplit.go:52).
func (s *Scene) sessionsShare(room int) int {
	avail := room - s.px(toolkit.SplitterHandleW)
	lo := s.px(PaneMinH) - 2*s.px(cardPad) // a pane's minimum, less its own padding
	return max(min(avail*s.vm.Split.Get()/SplitScale, avail-lo), lo)
}

// panelLayout is the scene.go:492-510 excerpt.
func (s *Scene) panelLayout(rows int) {
	px := s.px
	room := s.h - 2*px(margin) - px(headerH) - toolkit.TouchTarget(px(toolkit.NotebookTabStripH)) - px(toolkit.StatusbarH) - 5*px(gap) - 4*px(cardPad) - px(controlsH)
	inner := s.sessionsShare(room) - px(28) // the Sessions card's share, less its title
	spare := inner - px(minTableH) - rows*px(controlsH)
	maxModelRows := max(spare/(px(controlsH)+px(gap/3)), 0)
	_ = maxModelRows
	filterH := rows*px(controlsH) + (rows-1)*px(gap/3)

	s.filtersShown = inner >= filterH+px(gap/2)+px(minTableFloor)
}

// updateDetailChars is the scene.go:594-600 excerpt.
func (s *Scene) updateDetailChars() {
	avg := max(toolkit.TextWidth("abcdefghijklmnopqrstuvwxyz0123456789 ")/38, 1)
	room := s.px(s.vm.EffectivePanelW()) - 4*s.px(cardPad) - s.px(28) // two card pads a side, the scrollbar
	s.vm.DetailChars.Set(max(room/avg, minDetailChars))
}

// updateGuardChars is the scene_guard.go:81-86 excerpt.
func (s *Scene) updateGuardChars() {
	avg := max(toolkit.TextWidth("abcdefghijklmnopqrstuvwxyz0123456789 ")/38, 1)
	room := s.w*2/5 - 4*s.px(cardPad) - s.px(28)
	s.vm.Guard.Chars.Set(max(room/avg, minDetailChars))
}
