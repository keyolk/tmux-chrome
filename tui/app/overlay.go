package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/tuikit/fuzzy"
	"github.com/keyolk/tuikit/input"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/list"
	"github.com/keyolk/tuikit/render"
)

// overlay is a modal over the panes. It owns the keyboard and the mouse
// while open. Callbacks receive the model as it is when they run, never the
// one from when the overlay opened, and they get ids to act on: a target is
// re-resolved against the newest snapshot at the moment the user confirms.
type overlay interface {
	ctx() keys.Ctx
	key(m Model, msg tea.KeyMsg) (Model, tea.Cmd)
	view(m Model, w, h int) string
}

type action func(Model) (Model, tea.Cmd)

func closeOverlay(m Model) Model {
	m.ov = nil
	return m
}

// --- confirm ---

type confirmOv struct {
	title string
	lines []string
	yes   action
}

func (o confirmOv) ctx() keys.Ctx { return ctxConfirm }

func (o confirmOv) key(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	b, ok := m.km.Resolve([]keys.Ctx{ctxConfirm}, msg, m.keyOpts())
	if !ok {
		return m, nil
	}
	switch b.Action {
	case "quit":
		return m.quit()
	case "confirm.yes":
		return o.yes(closeOverlay(m))
	case "confirm.no":
		return closeOverlay(m), nil
	}
	return m, nil
}

// confirmShown caps the target list; the rest are counted, never hidden.
const confirmShown = 12

func (o confirmOv) view(m Model, w, h int) string {
	bw := min(max(44, w*2/3), w-2)
	cw := bw - 4
	var body []string
	for i, l := range o.lines {
		if i == confirmShown {
			body = append(body, m.th.Muted.Render(fmt.Sprintf("%s 외 %d개", render.Ellipsis(), len(o.lines)-confirmShown)))
			break
		}
		body = append(body, render.Truncate(l, cw))
	}
	body = append(body, "", m.km.Footer([]keys.Ctx{ctxConfirm}, cw, m.keyOpts(), m.th.FooterStyle()))
	bh := min(len(body)+2, h-2)
	return render.Box(o.title, strings.Join(body, "\n"), bw, bh, m.th.BoxFocus)
}

// --- prompt ---

type promptOv struct {
	title  string
	field  input.Field
	submit func(Model, string) (Model, tea.Cmd)
}

func (o promptOv) ctx() keys.Ctx { return ctxPrompt }

func (o promptOv) key(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	if b, ok := m.km.ResolveRaw([]keys.Ctx{ctxPrompt}, msg, m.keyOpts()); ok {
		switch b.Action {
		case "quit":
			return m.quit()
		case "prompt.ok":
			v := strings.TrimSpace(o.field.Value())
			if v == "" {
				return m, nil
			}
			return o.submit(closeOverlay(m), v)
		case "prompt.cancel":
			return closeOverlay(m), nil
		}
		return m, nil
	}
	if f, used := o.field.Update(msg); used {
		o.field = f
		m.ov = o
	}
	return m, nil
}

func (o promptOv) view(m Model, w, h int) string {
	bw := min(max(44, w/2), w-2)
	cw := bw - 4
	body := o.field.View(cw, true, input.Style{Placeholder: m.th.Subtle}) + "\n\n" +
		m.km.Footer([]keys.Ctx{ctxPrompt}, cw, m.keyOpts(), m.th.FooterStyle())
	return render.Box(o.title, body, bw, 5, m.th.BoxFocus)
}

// --- picker ---

type pickItem struct {
	key    string
	label  string
	detail string
	hit    []int
}

func (p pickItem) ID() string { return p.key }

type pickerOv struct {
	title  string
	items  []pickItem
	field  input.Field
	rows   list.List[pickItem]
	choose func(Model, pickItem) (Model, tea.Cmd)
}

func newPicker(title string, items []pickItem, choose func(Model, pickItem) (Model, tea.Cmd)) pickerOv {
	o := pickerOv{title: title, items: items, choose: choose, field: input.Field{Placeholder: "거르기"}}
	return o.filter()
}

func (o pickerOv) filter() pickerOv {
	q := o.field.Value()
	if q == "" {
		o.rows = o.rows.SetItems(o.items)
		return o
	}
	labels := make([]string, len(o.items))
	for i, it := range o.items {
		labels[i] = it.label + " " + it.detail
	}
	var out []pickItem
	for _, mt := range fuzzy.Filter(labels, q) {
		it := o.items[mt.Index]
		_, it.hit, _ = fuzzy.Score(it.label, q)
		out = append(out, it)
	}
	o.rows = o.rows.SetItems(out).Top()
	return o
}

func (o pickerOv) ctx() keys.Ctx { return ctxPicker }

func (o pickerOv) key(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	if b, ok := m.km.ResolveRaw([]keys.Ctx{ctxPicker}, msg, m.keyOpts()); ok {
		switch b.Action {
		case "quit":
			return m.quit()
		case "picker.ok":
			it, ok := o.rows.Selected()
			if !ok {
				return m, nil
			}
			return o.choose(closeOverlay(m), it)
		case "picker.cancel":
			return closeOverlay(m), nil
		case "picker.down":
			o.rows = o.rows.Move(1)
		case "picker.up":
			o.rows = o.rows.Move(-1)
		}
		m.ov = o
		return m, nil
	}
	if f, used := o.field.Update(msg); used {
		o.field = f
		m.ov = o.filter()
	}
	return m, nil
}

func (o pickerOv) view(m Model, w, h int) string {
	bw := min(max(50, w*2/3), w-2)
	bh := min(max(8, len(o.items)+5), h-2)
	cw, ch := render.BoxContent(bw, bh, m.th.BoxFocus)
	listH := max(1, ch-3)
	o.rows = o.rows.SetHeight(listH)
	lines := []string{o.field.View(cw, true, input.Style{Placeholder: m.th.Subtle}), ""}
	start, end := o.rows.Window()
	items := o.rows.Items()
	for i := start; i < end; i++ {
		it := items[i]
		label := highlightRunes(it.label, it.hit, m.th.Accent)
		if it.detail != "" {
			label += "  " + m.th.Muted.Render(it.detail)
		}
		line := "  " + label
		if it.key == o.rows.SelectedID() {
			line = m.th.Selected.Render(render.Pad(m.th.Glyphs.Cursor+" "+render.Truncate(label, cw-2), cw))
		}
		lines = append(lines, render.Truncate(line, cw))
	}
	if len(items) == 0 {
		lines = append(lines, m.th.Muted.Render("  맞는 항목이 없습니다"))
	}
	for len(lines) < ch-1 {
		lines = append(lines, "")
	}
	lines = append(lines[:ch-1], m.km.Footer([]keys.Ctx{ctxPicker}, cw, m.keyOpts(), m.th.FooterStyle()))
	return render.Box(o.title, strings.Join(lines, "\n"), bw, bh, m.th.BoxFocus)
}

// --- checklist ---

type checkItem struct {
	key    string
	label  string
	detail string
	tag    string
	data   any
}

func (c checkItem) ID() string { return c.key }

type checklistOv struct {
	title   string
	note    string
	rows    list.List[checkItem]
	checked map[string]bool
	submit  func(Model, []checkItem) (Model, tea.Cmd)
}

func newChecklist(title, note string, items []checkItem, checked map[string]bool, submit func(Model, []checkItem) (Model, tea.Cmd)) checklistOv {
	if checked == nil {
		checked = map[string]bool{}
	}
	return checklistOv{title: title, note: note, rows: list.New(items), checked: checked, submit: submit}
}

func (o checklistOv) ctx() keys.Ctx { return ctxChecklist }

func (o checklistOv) key(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	b, ok := m.km.Resolve([]keys.Ctx{ctxChecklist}, msg, m.keyOpts())
	if !ok {
		return m, nil
	}
	switch b.Action {
	case "quit":
		return m.quit()
	case "check.cancel":
		return closeOverlay(m), nil
	case "check.ok":
		var picked []checkItem
		for _, it := range o.rows.Items() {
			if o.checked[it.key] {
				picked = append(picked, it)
			}
		}
		if len(picked) == 0 {
			return m, nil
		}
		return o.submit(closeOverlay(m), picked)
	case "check.toggle":
		if it, ok := o.rows.Selected(); ok {
			o.checked = cloneChecked(o.checked)
			o.checked[it.key] = !o.checked[it.key]
			o.rows = o.rows.Move(1)
		}
	case "check.all":
		all := true
		for _, it := range o.rows.Items() {
			all = all && o.checked[it.key]
		}
		o.checked = map[string]bool{}
		for _, it := range o.rows.Items() {
			o.checked[it.key] = !all
		}
	case "check.down":
		o.rows = o.rows.Move(1)
	case "check.up":
		o.rows = o.rows.Move(-1)
	}
	m.ov = o
	return m, nil
}

func cloneChecked(c map[string]bool) map[string]bool {
	out := make(map[string]bool, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out
}

func (o checklistOv) view(m Model, w, h int) string {
	bw := min(max(60, w*3/4), w-2)
	bh := min(max(8, o.rows.Len()+6), h-2)
	cw, ch := render.BoxContent(bw, bh, m.th.BoxFocus)
	n := 0
	for _, it := range o.rows.Items() {
		if o.checked[it.key] {
			n++
		}
	}
	head := fmt.Sprintf("%d / %d 체크", n, o.rows.Len())
	if o.note != "" {
		head += "  " + m.th.Warn.Render(o.note)
	}
	lines := []string{render.Truncate(head, cw), ""}
	o.rows = o.rows.SetHeight(max(1, ch-3))
	start, end := o.rows.Window()
	items := o.rows.Items()
	for i := start; i < end; i++ {
		it := items[i]
		mark := m.th.Glyphs.Unmarked
		if o.checked[it.key] {
			mark = m.th.Accent.Render(m.th.Glyphs.Marked)
		}
		text := it.label
		if it.tag != "" {
			text = m.th.Muted.Render("["+it.tag+"]") + " " + text
		}
		if it.detail != "" {
			text += "  " + m.th.Muted.Render(it.detail)
		}
		line := mark + " " + render.Truncate(text, cw-2)
		if it.key == o.rows.SelectedID() {
			line = m.th.Selected.Render(render.Pad(line, cw))
		}
		lines = append(lines, line)
	}
	for len(lines) < ch-1 {
		lines = append(lines, "")
	}
	lines = append(lines[:ch-1], m.km.Footer([]keys.Ctx{ctxChecklist}, cw, m.keyOpts(), m.th.FooterStyle()))
	return render.Box(o.title, strings.Join(lines, "\n"), bw, bh, m.th.BoxFocus)
}
