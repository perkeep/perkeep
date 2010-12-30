package error

type StructuralError string

func (s StructuralError) String() string {
	return "OpenPGP data invalid: " + string(s)
}

type Unsupported string

func (s Unsupported) String() string {
	return "OpenPGP feature unsupported: " + string(s)
}
