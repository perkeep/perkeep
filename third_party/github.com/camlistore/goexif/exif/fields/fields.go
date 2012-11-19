package fields

type Std struct {
	ImageWidth
	ImageLength
	BitsPerSample
	Compression
	PhotometricInterpretation
	Orientation
	SamplesPerPixel
	PlanarConfiguration
	YCbCrSubSampling
	YCbCrPositioning
	XResolution
	YResolution
	ResolutionUnit
	DateTime
	ImageDescription
	Make
	Model
	Software
	Artist
	Copyright
	ExifIFDPointer
}

type Sub struct {
	GPSInfoIFDPointer
	InteroperabilityIFDPointer
	ExifVersion
	FlashpixVersion
	ColorSpace
	ComponentsConfiguration
	CompressedBitsPerPixel
	PixelXDimension
	PixelYDimension
	MakerNote
	UserComment
	RelatedSoundFile
	DateTimeOriginal
	DateTimeDigitized
	SubSecTime
	SubSecTimeOriginal
	SubSecTimeDigitized
	ImageUniqueID
	ExposureTime
	FNumber
	ExposureProgram
	SpectralSensitivity
	ISOSpeedRatings
	OECF
	ShutterSpeedValue
	ApertureValue
	BrightnessValue
	ExposureBiasValue
	MaxApertureValue
	SubjectDistance
	MeteringMode
	LightSource
	Flash
	FocalLength
	SubjectArea
	FlashEnergy
	SpatialFrequencyResponse
	FocalPlaneXResolution
	FocalPlaneYResolution
	FocalPlaneResolutionUnit
	SubjectLocation
	ExposureIndex
	SensingMethod
	FileSource
	SceneType
	CFAPattern
	CustomRendered
	ExposureMode
	WhiteBalance
	DigitalZoomRatio
	FocalLengthIn35mmFilm
	SceneCaptureType
	GainControl
	Contrast
	Saturation
	Sharpness
	DeviceSettingDescription
	SubjectDistanceRange
}

type GPS struct {
	GPSVersionID
	GPSLatitudeRef
	GPSLatitude
	GPSLongitudeRef
	GPSLongitude
	GPSAltitudeRef
	GPSAltitude
	GPSTimeStamp
	GPSSatelites
	GPSStatus
	GPSMeasureMode
	GPSDOP
	GPSSpeedRef
	GPSSpeed
	GPSTrackRef
	GPSTrack
	GPSImgDirectionRef
	GPSImgDirection
	GPSMapDatum
	GPSDestLatitudeRef
	GPSDestLatitude
	GPSDestLongitudeRef
	GPSDestLongitude
	GPSDestBearingRef
	GPSDestBearing
	GPSDestDistanceRef
	GPSDestDistance
	GPSProcessingMethod
	GPSAreaInformation
	GPSDateStamp
	GPSDifferential
}

type InterOp struct {
	InteroperabilityIndex
}

type Fields struct {
	Std
	Sub
	GPS
}

func New(x exif.Exif) *Fields {

}
