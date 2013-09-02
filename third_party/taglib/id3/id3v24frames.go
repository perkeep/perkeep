package id3

func GetId3v24TextIdentificationFrame(frame *Id3v24Frame) ([]string, error) {
	return getTextIdentificationFrame(frame.Content)
}
