package react

type Event interface{}

type OnChange interface {
	Event

	OnChange(e *SyntheticEvent)
}

type OnClick interface {
	Event

	OnClick(e *SyntheticMouseEvent)
}
