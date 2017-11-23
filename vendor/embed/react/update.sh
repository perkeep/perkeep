#!/bin/sh

REL=15.6.2

curl -O https://unpkg.com/react@${REL}/dist/react-with-addons.min.js
curl -O https://unpkg.com/react-dom@${REL}/dist/react-dom.min.js
curl -O https://raw.githubusercontent.com/facebook/react/v${REL}/LICENSE
curl -O https://raw.githubusercontent.com/facebook/react/v${REL}/README.md

# Useful for development, contains full warnings.
# curl -O https://unpkg.com/react@${REL}/dist/react-with-addons.js
# curl -O https://unpkg.com/react-dom@${REL}/dist/react-dom.js
