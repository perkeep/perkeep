# Configuring Geocoding

Geocoding is the process of converting a location name (like `nyc` or
`Argentina`) into GPS coordinates and bounding box(es).

Perkeep's location search will use Google's Geocoding API if a key is provided,
otherwise it falls back to using OpenStreetMap's API.

To use Google's Geocoding API, you need to manually get your own API key from
Google and place it in your Perkeep configuration directory (run `pk env
configdir` to find your configuration directory) in a file named
`google-geocode.key`.

To get the Google API key, see [Set up the Geocoding API](https://developers.google.com/maps/documentation/geocoding/get-api-key).
