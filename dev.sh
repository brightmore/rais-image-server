#!/bin/bash
set -eu

docker rm -f rais || true
docker run --rm -v $(pwd):/opt/rais-src rais-build make
docker run -it --rm \
  --name rais \
  --privileged=true \
  -p 12415:12415 \
  -v $(pwd):/opt/rais-src \
  rais-build /opt/rais-src/bin/rais-server --address ":12415" \
      --iiif-url "http://localhost:12415/iiif" \
      --tile-path "/var/local/images"
