#!/usr/bin/bash

# Get the current username
USER_NAME=$(whoami)
OPT_DIR="/opt/discord_gateway/$USER_NAME"

if [ -f "$OPT_DIR/.env" ] && [ -f "$OPT_DIR/records.csv" ]; then
    echo "Files are already present in $OPT_DIR. Skipping initialization."
else
    # Create the directory under /opt and assign ownership to you
    sudo mkdir -p $OPT_DIR
    sudo chown -R $USER_NAME:$USER_NAME $OPT_DIR

    # Restrict the folder permissions so only YOU can enter it
    sudo chmod 700 $OPT_DIR

    # Move your .env to the new location and create the log file
    if [ -f ".env" ] && [ ! -L ".env" ]; then
        mv .env $OPT_DIR/
    fi
    touch $OPT_DIR/records.csv

    # Restrict read/write permissions on the files to only your user
    chmod 600 $OPT_DIR/.env $OPT_DIR/records.csv

    echo "Setup complete! The app will now look for your config at $OPT_DIR/.env"
fi

# Ask user if they want to create a symlink if they don't already exist
if [ ! -L ".env" ] || [ ! -L "records.csv" ]; then
    read -p "Do you want to create symlinks in the current directory for .env and records.csv? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        if [ ! -e ".env" ]; then
            ln -s $OPT_DIR/.env .env
            echo "Created symlink for .env"
        fi
        if [ ! -e "records.csv" ]; then
            ln -s $OPT_DIR/records.csv records.csv
            echo "Created symlink for records.csv"
        fi
    fi
fi
