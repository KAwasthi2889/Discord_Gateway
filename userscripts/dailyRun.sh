#!/bin/bash

# Path to the directory containing discord_wss on your EC2 instance.
# IMPORTANT: Change this to the actual directory path where your project is deployed.
APP_DIR="$HOME/Discord_Gateway"
BINARY_NAME="discord"

# Check if the process is already running
if pgrep -x "$BINARY_NAME" > /dev/null
then
    echo "[$(date)] $BINARY_NAME is already running."
else
    echo "[$(date)] $BINARY_NAME is not running. Starting..."
    cd "$APP_DIR" || { echo "Failed to cd to $APP_DIR"; exit 1; }
    
    # Run the binary in the background and redirect output to a log file
    nohup ./"$BINARY_NAME" > "${BINARY_NAME}.log" 2>&1 &
    
    echo "[$(date)] Started $BINARY_NAME."
fi
