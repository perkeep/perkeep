package imm

import (
	"fmt"
	"strconv"

	"honnef.co/go/js/dom"

	r "myitcv.io/react"
)

const (
	noEntry = "-1"
)

//go:generate reactGen
//go:generate immutableGen

type Label interface {
	Label() string
}

type _Imm_LabelEntries []Label

type _Imm_strEntrySelect map[string]Label

type ImmSelectEntry interface {
	Range() []Label
}

// SelectDef is a wrapper around the myitcv.io/react.SelectDef component. It allows
// any value implementing the Label interface to be selected
//
type SelectDef struct {
	r.ComponentDef
}

type OnSelect interface {
	OnSelect(i Label)
}

type SelectProps struct {
	Entry   Label
	Entries ImmSelectEntry
	OnSelect
}

type SelectState struct {
	currEntry  string
	entries    *entriesKeysSelect
	entriesMap *strEntrySelect
}

type entryKey struct {
	key string
	p   Label
}

type _Imm_entriesKeysSelect []entryKey

// Select creates a new instance of the SelectDef component with the provided props
//
func Select(props SelectProps) *SelectElem {
	return buildSelectElem(props)
}

func (p SelectDef) ComponentWillMount() {
	p.updateMap(p.Props().Entries)
}

func (p SelectDef) ComponentWillReceiveProps(props SelectProps) {
	p.updateMap(props.Entries)
}

func (p SelectDef) updateMap(es ImmSelectEntry) {
	eks := newEntriesKeysSelect().AsMutable()
	defer eks.AsImmutable(nil)

	kem := newStrEntrySelect().AsMutable()
	defer kem.AsImmutable(nil)

	for i, p := range es.Range() {
		k := strconv.Itoa(i)
		kem.Set(k, p)
		eks.Append(entryKey{
			key: k,
			p:   p,
		})
	}

	kem.Set(noEntry, nil)

	st := p.State()
	st.entries = eks
	st.entriesMap = kem
	p.SetState(st)
}

func (p SelectDef) Render() r.Element {

	var ps []*r.OptionElem

	for _, v := range p.State().entries.Range() {
		p := r.Option(
			&r.OptionProps{Value: v.key},
			r.S(v.p.Label()),
		)

		ps = append(ps, p)
	}

	return r.Select(
		&r.SelectProps{
			Value:    p.State().currEntry,
			OnChange: changeEntry{p},
		},
		ps...,
	)
}

type changeEntry struct{ SelectDef }

func (c changeEntry) OnChange(e *r.SyntheticEvent) {
	v := e.Target().(*dom.HTMLSelectElement).Value

	p := c.SelectDef

	s := c.SelectDef.State()

	l, ok := p.State().entriesMap.Get(v)
	if !ok {
		panic(fmt.Errorf("Select component selected value %q that we don't know about", v))
	}

	s.currEntry = v
	p.SetState(s)

	p.Props().OnSelect.OnSelect(l)
}
