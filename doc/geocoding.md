# Configuring Geocoding

Geocoding is the process of converting a location name (like `nyc` or
`Argentina`) into GPS coordinates and bounding box(es).

Perkeep's location search currently requires Google's Geocoding API,
which now requires an API key. We do not currently provide a default,
shared API key to use by default. (We might in the future.)

For now, you need to manually get your own Geocoding API key from Google and place it
in your Perkeep configuration directory (run `pk env configdir` to
find your configuration directory) in a file named `google-geocode.key`.

To get the key, see https://developers.google.com/maps/documentation/geocoding/start#get-a-key
