package exif

type FieldName string

const (
	ImageWidth  FieldName = "ImageWidth"
	ImageLength FieldName = "ImageLength" // height
	Orientation FieldName = "Orientation"
)

var fields = map[FieldName]uint16{
	/////////////////////////////////////
	////////// IFD 0 ////////////////////
	/////////////////////////////////////

	// image data structure
	"ImageWidth":                0x0100,
	"ImageLength":               0x0101,
	"BitsPerSample":             0x0102,
	"Compression":               0x0103,
	"PhotometricInterpretation": 0x0106,
	"Orientation":               0x0112,
	"SamplesPerPixel":           0x0115,
	"PlanarConfiguration":       0x011C,
	"YCbCrSubSampling":          0x0212,
	"YCbCrPositioning":          0x0213,
	"XResolution":               0x011A,
	"YResolution":               0x011B,
	"ResolutionUnit":            0x0128,

	// Other tags
	"DateTime":         0x0132,
	"ImageDescription": 0x010E,
	"Make":             0x010F,
	"Model":            0x0110,
	"Software":         0x0131,
	"Artist":           0x010e,
	"Copyright":        0x010e,

	// private tags
	"ExifIFDPointer": exifPointer,

	/////////////////////////////////////
	////////// Exif sub IFD /////////////
	/////////////////////////////////////

	"GPSInfoIFDPointer":          gpsPointer,
	"InteroperabilityIFDPointer": interopPointer,

	"ExifVersion":     0x9000,
	"FlashpixVersion": 0xA000,

	"ColorSpace": 0xA001,

	"ComponentsConfiguration": 0x9101,
	"CompressedBitsPerPixel":  0x9102,
	"PixelXDimension":         0xA002,
	"PixelYDimension":         0xA003,

	"MakerNote":   0x927C,
	"UserComment": 0x9286,

	"RelatedSoundFile":    0xA004,
	"DateTimeOriginal":    0x9003,
	"DateTimeDigitized":   0x9004,
	"SubSecTime":          0x9290,
	"SubSecTimeOriginal":  0x9291,
	"SubSecTimeDigitized": 0x9292,

	"ImageUniqueID": 0xA420,

	// picture conditions
	"ExposureTime":             0x829A,
	"FNumber":                  0x829D,
	"ExposureProgram":          0x8822,
	"SpectralSensitivity":      0x8824,
	"ISOSpeedRatings":          0x8827,
	"OECF":                     0x8828,
	"ShutterSpeedValue":        0x9201,
	"ApertureValue":            0x9202,
	"BrightnessValue":          0x9203,
	"ExposureBiasValue":        0x9204,
	"MaxApertureValue":         0x9205,
	"SubjectDistance":          0x9206,
	"MeteringMode":             0x9207,
	"LightSource":              0x9208,
	"Flash":                    0x9209,
	"FocalLength":              0x920A,
	"SubjectArea":              0x9214,
	"FlashEnergy":              0xA20B,
	"SpatialFrequencyResponse": 0xA20C,
	"FocalPlaneXResolution":    0xA20E,
	"FocalPlaneYResolution":    0xA20F,
	"FocalPlaneResolutionUnit": 0xA210,
	"SubjectLocation":          0xA214,
	"ExposureIndex":            0xA215,
	"SensingMethod":            0xA217,
	"FileSource":               0xA300,
	"SceneType":                0xA301,
	"CFAPattern":               0xA302,
	"CustomRendered":           0xA401,
	"ExposureMode":             0xA402,
	"WhiteBalance":             0xA403,
	"DigitalZoomRatio":         0xA404,
	"FocalLengthIn35mmFilm":    0xA405,
	"SceneCaptureType":         0xA406,
	"GainControl":              0xA407,
	"Contrast":                 0xA408,
	"Saturation":               0xA409,
	"Sharpness":                0xA40A,
	"DeviceSettingDescription": 0xA40B,
	"SubjectDistanceRange":     0xA40C,

	/////////////////////////////////////
	//// GPS sub-IFD ////////////////////
	/////////////////////////////////////
	"GPSVersionID":        0x0,
	"GPSLatitudeRef":      0x1,
	"GPSLatitude":         0x2,
	"GPSLongitudeRef":     0x3,
	"GPSLongitude":        0x4,
	"GPSAltitudeRef":      0x5,
	"GPSAltitude":         0x6,
	"GPSTimeStamp":        0x7,
	"GPSSatelites":        0x8,
	"GPSStatus":           0x9,
	"GPSMeasureMode":      0xA,
	"GPSDOP":              0xB,
	"GPSSpeedRef":         0xC,
	"GPSSpeed":            0xD,
	"GPSTrackRef":         0xE,
	"GPSTrack":            0xF,
	"GPSImgDirectionRef":  0x10,
	"GPSImgDirection":     0x11,
	"GPSMapDatum":         0x12,
	"GPSDestLatitudeRef":  0x13,
	"GPSDestLatitude":     0x14,
	"GPSDestLongitudeRef": 0x15,
	"GPSDestLongitude":    0x16,
	"GPSDestBearingRef":   0x17,
	"GPSDestBearing":      0x18,
	"GPSDestDistanceRef":  0x19,
	"GPSDestDistance":     0x1A,
	"GPSProcessingMethod": 0x1B,
	"GPSAreaInformation":  0x1C,
	"GPSDateStamp":        0x1D,
	"GPSDifferential":     0x1E,

	/////////////////////////////////////
	//// Interoperability sub-IFD ///////
	/////////////////////////////////////
	"InteroperabilityIndex": 0x1,
}
