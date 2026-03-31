#!/bin/bash
CURDIR=$(cd $(dirname $0); pwd)
BinaryName=psych.post
echo "$CURDIR/bin/${BinaryName}"
exec $CURDIR/bin/${BinaryName}