#!/bin/bash

# Build the binary
echo "Building logviewer binary..."
go build -o logviewer main.go

# Make it executable
chmod +x logviewer

echo "Build complete! You can now use the logviewer with these options:"
echo
echo "View all logs (default):"
echo "  ./logviewer <path-to-log-file>"
echo
echo "View specific log levels (can be combined):"
echo "  ./logviewer -info <path-to-log-file>     # Show only INFO logs"
echo "  ./logviewer -warn <path-to-log-file>     # Show only WARN logs"
echo "  ./logviewer -notice <path-to-log-file>   # Show only NOTICE logs"
echo "  ./logviewer -debug <path-to-log-file>    # Show only DEBUG logs"
echo "  ./logviewer -err <path-to-log-file>      # Show only ERROR logs"
echo
echo "View multiple levels:"
echo "  ./logviewer -warn -err <path-to-log-file>                # Show WARN and ERROR logs"
echo "  ./logviewer -info -notice -debug <path-to-log-file>      # Show INFO, NOTICE, and DEBUG logs"
echo
echo "Explicitly view all levels:"
echo "  ./logviewer -all <path-to-log-file>" 