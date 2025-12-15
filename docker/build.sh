#!/bin/bash
#curl -L https://7ff.org/lib.tgz -o pkg/objectPredict/lib.tgz
docker buildx build --no-cache  -t 192.168.102.29:89/8fforg/firescrew:latest -f docker/Dockerfile .
