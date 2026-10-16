/*
Copyright 2026 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package index

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"

	"perkeep.org/pkg/blob"
	"perkeep.org/pkg/schema"
	"perkeep.org/pkg/types/camtypes"
)

// GetEXIF returns a file's indexed EXIF tags, ordered by directory (IFD0, IFD1,
// Exif, GPS) and tag number. Malformed optional EXIF rows are ignored.
// The caller must hold x's read lock.
func (x *Index) GetEXIF(ctx context.Context, fileRef blob.Ref) (_ *camtypes.EXIFInfo, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !fileRef.Valid() {
		return nil, os.ErrNotExist
	}
	bm, err := x.GetBlobMeta(ctx, fileRef)
	if err != nil {
		return nil, err
	}
	if bm.CamliType != schema.TypeFile {
		return nil, os.ErrNotExist
	}
	fi, err := x.GetFileInfo(ctx, fileRef)
	if err != nil {
		return nil, err
	}
	res := &camtypes.EXIFInfo{
		FileRef:  fileRef,
		WholeRef: fi.WholeRef,
		Tags:     []camtypes.EXIFTag{},
	}
	if !fi.WholeRef.Valid() {
		return res, nil
	}

	// Full EXIF is deliberately not held in the corpus. Always read its
	// optional rows from storage, even if file metadata came from the corpus.
	prefix := keyEXIFTag.Prefix(fi.WholeRef)
	it := x.queryPrefixString(prefix)
	defer closeIterator(it, &err)
	for it.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if tag, ok := parseEXIFTag(strings.TrimPrefix(it.Key(), prefix), it.Value()); ok {
			res.Tags = append(res.Tags, tag)
		}
	}
	return res, nil
}

var exifDirectories = [...]string{"IFD0", "IFD1", "Exif", "GPS"}

// key is the directory and tag suffix of an exiftag key.
func parseEXIFTag(key, value string) (camtypes.EXIFTag, bool) {
	k := strings.Split(key, "|")
	if len(k) != 2 || len(k[0]) != 1 || k[0][0] < '0' || k[0][0] > '3' ||
		len(k[1]) != 4 || strings.Trim(k[1], "0123456789abcdef") != "" {
		return camtypes.EXIFTag{}, false
	}
	dir := int(k[0][0] - '0')
	v := strings.Split(value, "|")
	if len(v) < 3 {
		return camtypes.EXIFTag{}, false
	}
	n, err := strconv.ParseUint(v[1], 10, 32)
	if err != nil {
		return camtypes.EXIFTag{}, false
	}
	values := v[2:]
	if n == 0 && len(values) == 1 && values[0] == "" {
		values = []string{}
	}
	if uint64(len(values)) != n {
		return camtypes.EXIFTag{}, false
	}
	switch v[0] {
	case "string":
		if n != 1 {
			return camtypes.EXIFTag{}, false
		}
		values[0], err = url.QueryUnescape(values[0])
		if err != nil {
			return camtypes.EXIFTag{}, false
		}
	case "int", "rat", "float":
		for _, s := range values {
			switch v[0] {
			case "int":
				_, err = strconv.ParseInt(s, 10, 64)
			case "float":
				_, err = strconv.ParseFloat(s, 64)
			case "rat":
				parts := strings.Split(s, "/")
				if len(parts) != 2 {
					return camtypes.EXIFTag{}, false
				}
				_, err = strconv.ParseInt(parts[0], 10, 64)
				if err == nil {
					_, err = strconv.ParseInt(parts[1], 10, 64)
				}
			}
			if err != nil {
				return camtypes.EXIFTag{}, false
			}
		}
	default:
		return camtypes.EXIFTag{}, false
	}
	return camtypes.EXIFTag{
		Directory: exifDirectories[dir],
		Tag:       k[1],
		Name:      exifTagName(dir, k[1]),
		Type:      v[0],
		Values:    values,
	}, true
}

// goexif's tag-number-to-name maps are unexported; its exported field-name
// constants alone cannot resolve indexed numeric tags. Keep common names here
// without decoding synthetic images or conflating the GPS and image namespaces.
func exifTagName(dir int, tag string) string {
	names := exifImageTagNames
	switch dir {
	case exifIFD:
		names = exifSubIFDTagNames
	case exifIFDGPS:
		names = exifGPSTagNames
	}
	if name := names[tag]; name != "" {
		return name
	}
	return "Unknown tag"
}

var exifImageTagNames = map[string]string{
	"0100": "ImageWidth", "0101": "ImageLength", "0102": "BitsPerSample",
	"0103": "Compression", "0106": "PhotometricInterpretation",
	"010e": "ImageDescription", "010f": "Make", "0110": "Model",
	"0111": "StripOffsets", "0112": "Orientation", "0115": "SamplesPerPixel",
	"0116": "RowsPerStrip", "0117": "StripByteCounts",
	"011a": "XResolution", "011b": "YResolution", "011c": "PlanarConfiguration",
	"0128": "ResolutionUnit", "0131": "Software", "0132": "DateTime",
	"013b": "Artist", "0201": "JPEGInterchangeFormat", "0202": "JPEGInterchangeFormatLength",
	"0212": "YCbCrSubSampling", "0213": "YCbCrPositioning", "8298": "Copyright",
	"8769": "ExifIFDPointer", "8825": "GPSInfoIFDPointer",
	"9c9b": "XPTitle", "9c9c": "XPComment", "9c9d": "XPAuthor",
	"9c9e": "XPKeywords", "9c9f": "XPSubject",
}

var exifSubIFDTagNames = map[string]string{
	"829a": "ExposureTime", "829d": "FNumber", "8822": "ExposureProgram",
	"8824": "SpectralSensitivity", "8827": "ISOSpeedRatings", "8828": "OECF",
	"8830": "SensitivityType", "8831": "StandardOutputSensitivity",
	"8832": "RecommendedExposureIndex", "8833": "ISOSpeed",
	"9000": "ExifVersion", "9003": "DateTimeOriginal", "9004": "DateTimeDigitized",
	"9010": "OffsetTime", "9011": "OffsetTimeOriginal", "9012": "OffsetTimeDigitized",
	"9101": "ComponentsConfiguration", "9102": "CompressedBitsPerPixel",
	"9201": "ShutterSpeedValue", "9202": "ApertureValue", "9203": "BrightnessValue",
	"9204": "ExposureBiasValue", "9205": "MaxApertureValue", "9206": "SubjectDistance",
	"9207": "MeteringMode", "9208": "LightSource", "9209": "Flash", "920a": "FocalLength",
	"9214": "SubjectArea", "927c": "MakerNote", "9286": "UserComment",
	"9290": "SubSecTime", "9291": "SubSecTimeOriginal", "9292": "SubSecTimeDigitized",
	"a000": "FlashpixVersion", "a001": "ColorSpace",
	"a002": "PixelXDimension", "a003": "PixelYDimension", "a004": "RelatedSoundFile",
	"a005": "InteroperabilityIFDPointer", "a20b": "FlashEnergy",
	"a20c": "SpatialFrequencyResponse", "a20e": "FocalPlaneXResolution",
	"a20f": "FocalPlaneYResolution", "a210": "FocalPlaneResolutionUnit",
	"a214": "SubjectLocation", "a215": "ExposureIndex", "a217": "SensingMethod",
	"a300": "FileSource", "a301": "SceneType", "a302": "CFAPattern",
	"a401": "CustomRendered", "a402": "ExposureMode", "a403": "WhiteBalance",
	"a404": "DigitalZoomRatio", "a405": "FocalLengthIn35mmFilm",
	"a406": "SceneCaptureType", "a407": "GainControl", "a408": "Contrast",
	"a409": "Saturation", "a40a": "Sharpness", "a40b": "DeviceSettingDescription",
	"a40c": "SubjectDistanceRange", "a420": "ImageUniqueID",
	"a430": "CameraOwnerName", "a431": "BodySerialNumber", "a432": "LensSpecification",
	"a433": "LensMake", "a434": "LensModel", "a435": "LensSerialNumber",
}

var exifGPSTagNames = map[string]string{
	"0000": "GPSVersionID", "0001": "GPSLatitudeRef", "0002": "GPSLatitude",
	"0003": "GPSLongitudeRef", "0004": "GPSLongitude", "0005": "GPSAltitudeRef",
	"0006": "GPSAltitude", "0007": "GPSTimeStamp", "0008": "GPSSatellites",
	"0009": "GPSStatus", "000a": "GPSMeasureMode", "000b": "GPSDOP",
	"000c": "GPSSpeedRef", "000d": "GPSSpeed", "000e": "GPSTrackRef",
	"000f": "GPSTrack", "0010": "GPSImgDirectionRef", "0011": "GPSImgDirection",
	"0012": "GPSMapDatum", "0013": "GPSDestLatitudeRef", "0014": "GPSDestLatitude",
	"0015": "GPSDestLongitudeRef", "0016": "GPSDestLongitude",
	"0017": "GPSDestBearingRef", "0018": "GPSDestBearing",
	"0019": "GPSDestDistanceRef", "001a": "GPSDestDistance",
	"001b": "GPSProcessingMethod", "001c": "GPSAreaInformation",
	"001d": "GPSDateStamp", "001e": "GPSDifferential", "001f": "GPSHPositioningError",
}
