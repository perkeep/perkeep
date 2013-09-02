package id3

func GetId3v23TextIdentificationFrame(frame *Id3v23Frame) ([]string, error) {
	return getTextIdentificationFrame(frame.Content)
}
