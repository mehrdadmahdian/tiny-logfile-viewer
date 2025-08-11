#!/bin/bash
set -e

go build -o hoapiLogViewerMacos
chmod +x hoapiLogViewerMacos

GOOS=linux GOARCH=amd64 go build -o hoapiLogViewerLinux
chmod +x hoapiLogViewerLinux