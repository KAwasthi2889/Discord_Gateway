# Discord Gateway Request Timeline

This document maps the entire lifecycle of a request from Discord message arrival through the userscript execution to the final callback in the Go application. This serves as a single source of truth for debugging, showing exactly where a request can be dropped, what conditions cause it to fail, and what logs are emitted at each stage based on the active codebase.

### 0. Gateway Startup Initialization
- **Port Allocation**: The Gateway binds to a dynamic callback port (`cbport`).
- **Userscript Verification**: 
  - Go adds a `/ping` endpoint to its callback server.
  - Go commands the browser to open `http://127.0.0.1:<cbport>/ping` and waits.
  - The userscript (`reviver.user.js`) will instantly catch this page, send a `fetch('/pong')`, and call `window.close()`.
  - *Failure Condition*: If Go does not receive `/pong` within 5 seconds, it logs a critical error ("CRITICAL ERROR: Userscript did not respond to /ping within 5 seconds...") and gracefully shuts down the Go application.

### 1. Message Ingestion
- **Arrival**: Message received via `Session.AddHandler(messageCreate)`.
- **Validation**: 
  - *Failure Condition*: If `m.ChannelID` does not match the observed channel, it is silently dropped.
- **Deserialization**:
  - *Failure Condition*: If JSON payload parsing fails, it is silently dropped.

### 2. Information Extraction
- **Regex Parsing**: Searches for `"torn.com/profiles.php?XID="`.
  - *Failure Condition*: If no match is found, returns `nil` and the request is silently dropped.
- **Data Capture**: Extracts `PlayerID`, `PlayerName`, `FactionName`, `FactionID`, and `TargetType` (regular vs premium).

### 3. Gateway Pre-flight Checks
- **Null ID Check**: 
  - *Failure Condition*: If `PlayerID == 0`, request is silently dropped.
- **Nuke Cache Shitlist Check**:
  - *Failure Condition*: If `IsShitlisted(PlayerID, FactionID)` returns true, drops request and logs: `[INFO] Request dropped silently reason="on shitlist" type={type} xid={xid}`.
- **Rate Limit Check (Per-Target & Global)**:
  - *Failure Condition*: If `IsAllowed(PlayerID)` returns false (too many rapid requests for the same ID), drops request and logs: `[INFO] Rate limit hit for target. Dropping request silently. target_id={xid}`.
  - *Failure Condition*: A hardcoded max of 15 tabs opened in a minute for all/any XID. If hit, drops request and logs: `[INFO] Global rate limit hit (>15/min). Dropping request silently.`
- **Daily Quota Check**:
  - *Failure Condition*: If `quota.IsLimitReached()` returns true, logs `[WARN] Daily quota limit reached. Dropping request silently and gracefully shutting down.` and **gracefully shuts down the Go application**.

### 4. Cache & Browser Launch
- **Cache Registration**: Adds `xid` to `PayloadCache` with a **25-second expiration**.
  - *Failure Condition*: If the userscript doesn't hit the callback server within 25 seconds, a background goroutine in Go deletes it and logs: `[WARN] Timeout / No response from browser for XID, flushing it xid={xid}`.
- **Browser Execution**: Generates URL with `?XID=...#autorevive=...&cbport=...` and launches the OS default browser.

### 5. Userscript Initialization
- **Context Validation**:
  - *Failure Condition*: If URL hash does not contain `#autorevive`, script immediately halts.
- **Target Container Search**: 
  - *Failure Condition*: Waits up to 10 seconds for `.buttons-list`. If not found, checks for the presence of specific elements (`.main-desc`) to determine the player's status:
    - "User is in Federal Jail"
    - "User is Traveling"
    - "User is not in hospital anymore" (e.g., text is "okay")
    - "User is in a different country's hospital"
    - "Not in Hospital, In a different country" (e.g., "hiding out in")
    If any of these specific states are found, it sends that exact string to the callback with `status=fail`.
    - *Unfamiliar Error Logic*: If `.main-desc` contains unknown text, it sends: `Unknown State: {raw text of main-desc}`.
    - If `.main-desc` doesn't exist at all, it sends: `Auto-revive timed out. Unable to load Button list.`
    In all cases, the script then instantly closes the tab.

### 6. Userscript Pre-Click Checks
- **Status Override Check**:
  - *Failure Condition*: If contract enforces `ONLINE` but player is `OFFLINE`, calls back with fail status: `[UserScript] Skipped auto-revive, player is {current}, but contract requires {required}.`
- **Age Minimum Check**:
  - *Failure Condition*: If player age is under `MIN_AGE_DAYS`, calls back with fail status: `[UserScript] Skipped auto-revive, player age {age} days is under {min} day minimum.`
- **Disabled/Cross Button State**:
  - *Failure Condition*: If button is disabled, watches it for 15 seconds. If it never becomes active, calls back with fail status: `[UserScript] Revive button remained disabled for 15s.`

### 7. Userscript Confirmation
- **Click Initial Button**: Clicks `.profile-button-revive`.
- **Threshold Matching**: Reads user's stored threshold from browser `localStorage` (`fastReviveSettings.threshold`). If missing, defaults to `60` and writes `{"threshold": 60}` back into `localStorage`.
- **Chance Extraction**: Reads the percentage from the confirmation dialog.
  - *Failure Condition*: If chance cannot be read, calls back with fail status: `[UserScript] Could not determine success chance.`
  - *Failure Condition*: If chance < effective threshold, calls back with fail status: `[UserScript] Skipped auto-revive, chance {x}% is below minChance {y}%.`
- **Confirm Click**: Clicks `.confirm-action-yes`.

### 8. Userscript Result Observation
- **Wait for Success Message**:
  - *Failure Condition*: Watches for **5 seconds** after clicking Yes. If no message appears, calls back with fail status: `[UserScript] Success message not found within 5s.`
- **Evaluate Message**: 
  - *Success Condition 1 (Actual Success)*: Reads "successfully revived" or `.t-green` class. Calls back with `status=success`.
  - *Success Condition 2 (Chance Failure)*: Reads `"attempted to revive"` and `"but failed"` anywhere in the text. Calls back with **`status=success`** (so Go increments the quota) and `reason=failed to revive`.
  - *Failure Condition*: Reads any other text. 
    - Specifically standardizes matches: `"Not enough energy"` (from "You do not have enough energy to perform this action.").
    - If it's an unfamiliar error dialog, returns the exact string of the unfamiliar error.
    Calls back with `status=fail` and the exact text as the `reason`.
- **Cleanup**: `window.close()` is called from inside the `GM_xmlhttpRequest` callback exactly 1 second after firing the request.

### 9. Gateway Callback Server
- **Cache Matching**:
  - *Failure Condition*: If `xid` is missing from cache (expired > 25s or already processed), returns `200 OK` and silently drops.
- **Handling Result**: 
  - The Go server applies no logic to standardizing error reasons; it logs exactly what the userscript sends.
  - If `status == "success"`:
    - **Records to `records.csv`** and increments the Daily Quota.
    - If `reason` contains "failed to revive", logs: `[INFO] Failed to revive xid={xid}`.
    - Otherwise, logs: `[INFO] Revive successful xid={xid}`.
  - If `status == "fail"`:
    - Logs `[INFO] Skipped auto-revive xid={xid} reason={reason}`.
    - *Does not* increment quota, *does not* write to `records.csv`.
  - **Emergency Shutdown**:
    - *Failure Condition*: If `reason` contains "Not enough energy", logs `[ERROR] CRITICAL: Out of energy detected! Initiating emergency gateway shutdown.` and gracefully terminates the application.
