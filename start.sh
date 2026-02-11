#!/bin/bash
cd /root/vantageselfservice
pkill -f './helpdesk' 2>/dev/null
sleep 1
nohup ./helpdesk > helpdesk.log 2>&1 &
sleep 2
if ss -tlnp | grep -q ':8080 '; then
    echo SERVICE_OK
else
    # Check if config corruption caused the failure
    if grep -q "cipher: message authentication failed" helpdesk.log 2>/dev/null; then
        echo "Config file corrupted, removing and retrying..."
        rm -f data/config.json
        pkill -f './helpdesk' 2>/dev/null
        sleep 1
        nohup ./helpdesk > helpdesk.log 2>&1 &
        sleep 2
        if ss -tlnp | grep -q ':8080 '; then
            echo SERVICE_OK
        else
            echo SERVICE_FAIL
            tail -5 helpdesk.log
        fi
    else
        echo SERVICE_FAIL
        tail -5 helpdesk.log
    fi
fi
