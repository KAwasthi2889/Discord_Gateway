// ==UserScript==
// @name         Torn Gateway Reviver
// @namespace    http://tampermonkey.net/
// @version      1.1.5
// @description  Event-driven auto-revives based on Discord Gateway callbacks.
// @author       Ever2889 [4040971]
// @match        https://www.torn.com/profiles.php*
// @match        http://127.0.0.1:*/ping*
// @match        http://localhost:*/ping*
// @icon         https://www.google.com/s2/favicons?sz=64&domain=torn.com
// @license      MIT
// @grant        GM_xmlhttpRequest
// @grant        window.close
// @connect      127.0.0.1
// @connect      localhost
// @run-at       document-idle
// ==/UserScript==

(function () {
    'use strict';

    if (window.location.pathname.startsWith('/ping')) {
        fetch('/pong').then(() => window.close()).catch(() => window.close());
        return;
    }

    const savedHash = window.location.hash;
    if (!savedHash.includes('autorevive')) {
        return; // Only run on gateway tabs
    }

    let isConfirming = false;
    let cbport = null;
    let gatewayXid = new URLSearchParams(window.location.search).get("XID");

    let minChanceOverride = 60; // Default
    try {
        const stored = localStorage.getItem('fastReviveSettings');
        if (stored) {
            const parsed = JSON.parse(stored);
            if (parsed.threshold !== undefined) minChanceOverride = parsed.threshold;
        } else {
            localStorage.setItem('fastReviveSettings', JSON.stringify({ threshold: 60 }));
        }
    } catch (e) { }
    let requiredStatus = null;
    let MIN_AGE_DAYS = 365;

    // Replace hash with #auto to prevent accidental re-triggers on manual refresh
    // while keeping a marker so fast_revive knows to stay away
    history.replaceState(null, '', window.location.pathname + window.location.search + '#auto');

    const portMatch = savedHash.match(/cbport=(\d+)/);
    if (portMatch) cbport = parseInt(portMatch[1], 10);

    const hashMatch = savedHash.match(/autorevive=(\d+)/);
    if (hashMatch) {
        const parsedAge = parseInt(hashMatch[1], 10);
        if (!isNaN(parsedAge) && parsedAge > 0) MIN_AGE_DAYS = parsedAge;
    }

    const minChanceMatch = savedHash.match(/minChance=(\d+)/);
    if (minChanceMatch) {
        const parsedChance = parseInt(minChanceMatch[1], 10);
        if (!isNaN(parsedChance) && parsedChance >= 0) minChanceOverride = parsedChance;
    }

    const statusMatch = savedHash.match(/status=([^&]+)/);
    if (statusMatch) requiredStatus = decodeURIComponent(statusMatch[1]).toUpperCase();

    function logToGateway(status, reason, overrideXid = null) {
        const xid = overrideXid || gatewayXid;
        if (cbport && xid) {
            const url = `http://127.0.0.1:${cbport}/revive?xid=${xid}&status=${status}&reason=${encodeURIComponent(reason)}&_t=${Date.now()}`;
            if (typeof GM_xmlhttpRequest !== "undefined") {
                GM_xmlhttpRequest({
                    method: "GET",
                    url: url,
                    onload: (response) => {
                        if (response.status !== 200) {
                            console.error(`UNEXPECTED ERROR: HTTP ${response.status} - ${response.responseText}`);
                        } else {
                            console.log(`[UserScript] Callback fired to port ${cbport}: status=${status}`);
                        }
                        setTimeout(() => window.close(), 1000);
                    },
                    onerror: (e) => {
                        console.error("UNEXPECTED ERROR: [UserScript] GM_xmlhttpRequest failed:", e);
                        setTimeout(() => window.close(), 1000);
                    }
                });
            } else {
                console.error("[UserScript] Fatal: GM_xmlhttpRequest not granted!");
                setTimeout(() => window.close(), 1000);
            }
        }
    }

    function getReviveInfo(container = document.body) {
        let pageText = "";
        const confirmDialog = container.querySelector('.confirm-revive');
        if (confirmDialog) {
            pageText = confirmDialog.innerText || confirmDialog.textContent;
        } else {
            const textEl = container.querySelector('.profile-buttons-dialog .text') || container.querySelector('div.text');
            if (textEl) pageText = textEl.textContent || textEl.innerText;
            else pageText = container.innerText || container.textContent;
        }

        const match = pageText.match(/(\d+(?:\.\d+)?)% chance of success/);
        const chance = match ? parseFloat(match[1]) : null;
        return { chance };
    }

    const watchForSuccessAndClose = () => {
        let successFound = false;

        const successObserver = new MutationObserver((m, obs) => {
            const responseTextEl = document.querySelector('.profile-buttons-dialog .center-block .text');
            if (responseTextEl) {
                const text = responseTextEl.textContent.trim();
                if (text.includes("chance of success")) return;

                const isSuccess = responseTextEl.classList.contains('t-green') || text.includes('successfully revived');
                const isChanceFailure = text.toLowerCase().includes('failed to revive');

                if (cbport && gatewayXid) {
                    if (isSuccess) {
                        logToGateway('success', '', gatewayXid);
                    } else if (isChanceFailure) {
                        logToGateway('success', 'failed to revive', gatewayXid);
                    } else {
                        let reason = text;
                        if (text.includes("You do not have enough energy to perform this action.")) {
                            reason = "Not enough energy";
                        }
                        logToGateway('fail', reason, gatewayXid);
                    }
                }
                successFound = true;
                obs.disconnect();
            }
        });

        const dialogTarget = document.querySelector('.profile-buttons-dialog');
        if (dialogTarget) {
            successObserver.observe(dialogTarget, { childList: true, subtree: true, characterData: true });
        } else {
            successObserver.observe(document.body, { childList: true, subtree: true, characterData: true });
        }

        setTimeout(() => {
            successObserver.disconnect();
            if (!successFound) {
                const msg = '[UserScript] Success message not found within 5s.';
                console.log(msg);
                logToGateway('fail', msg);
            }
        }, 5000);
    };

    function autoConfirmRevive() {
        if (isConfirming) return;
        const yesButton = document.querySelector('.confirm-action-yes') || document.querySelector('.confirm-action');
        if (yesButton) {
            const dialog = document.querySelector('.profile-buttons-dialog');
            const reviveInfo = getReviveInfo(dialog || document.body);
            if (reviveInfo.chance !== null) {
                if (reviveInfo.chance >= minChanceOverride) {
                    isConfirming = true;
                    yesButton.click();
                    watchForSuccessAndClose();
                } else {
                    logToGateway('fail', `[UserScript] Skipped auto-revive, chance ${reviveInfo.chance}% is below minChance ${minChanceOverride}%.`);
                }
            } else {
                logToGateway('fail', '[UserScript] Could not determine success chance.');
            }
        }
    }

    let debounceTimer;
    const observer = new MutationObserver((mutations) => {
        clearTimeout(debounceTimer);
        debounceTimer = setTimeout(() => {
            autoConfirmRevive();
        }, 50);
    });

    const getPlayerAgeDays = () => {
        const ttAge = document.querySelector('.tt-age-text');
        if (ttAge) {
            const text = ttAge.textContent.trim();
            let totalDays = 0;
            let matched = false;
            const years = text.match(/(\d+)\s*year/i);
            const months = text.match(/(\d+)\s*month/i);
            const days = text.match(/(\d+)\s*day/i);
            if (years) { totalDays += parseInt(years[1], 10) * 365; matched = true; }
            if (months) { totalDays += parseInt(months[1], 10) * 30; matched = true; }
            if (days) { totalDays += parseInt(days[1], 10); matched = true; }
            if (matched) return totalDays;
        }
        const ageBox = document.querySelector('.box-info.age');
        if (ageBox) {
            const digits = ageBox.querySelectorAll('.digit');
            let numStr = '';
            digits.forEach(d => { numStr += d.textContent.trim(); });
            const parsed = parseInt(numStr, 10);
            if (!isNaN(parsed)) return parsed;
        }
        return null;
    };

    const getPlayerStateError = () => {
        const descEl = document.querySelector('.main-desc');
        if (!descEl) return null;
        const text = descEl.textContent.trim().toLowerCase();
        if (text.includes("traveling")) return "Not in a hospital, Travelling";
        if (text === "okay") return "User is not in hospital anymore";
        if (text.includes("hospital")) {
            if (text.startsWith("in a ") && text.includes("hospital")) return "User is in a different country's hospital";
            return `UNEXPECTED ERROR: Unknown State: ${text}`;
        }
        if (text.startsWith("hiding out in") || text.startsWith("in ")) return "Not in Hospital, In a different country";
        return `UNEXPECTED ERROR: Unknown State: ${text}`;
    };

    const clickReviveButton = () => {
        const revButton = document.querySelector('.profile-button-revive');
        if (!revButton) {
            const specificError = getPlayerStateError();
            const msg = specificError ? `[UserScript] ${specificError}` : '[UserScript] Revive button disappeared while waiting.';
            logToGateway('fail', msg);
            return;
        }

        const executeClick = () => {
            setTimeout(() => {
                if (requiredStatus && requiredStatus !== 'ANY') {
                    const statusIcon = document.querySelector('li[class*="user-status-16-"]');
                    let currentStatus = "UNKNOWN";
                    if (statusIcon) {
                        const match = statusIcon.className.match(/user-status-16-([a-zA-Z]+)/);
                        if (match) currentStatus = match[1].toUpperCase();
                    }
                    if (currentStatus !== requiredStatus) {
                        const msg = `[UserScript] Skipped auto-revive, player is ${currentStatus}, but contract requires ${requiredStatus}.`;
                        logToGateway('fail', msg);
                        return;
                    }
                }

                const ageDays = getPlayerAgeDays();
                if (ageDays !== null && ageDays < MIN_AGE_DAYS) {
                    const msg = `[UserScript] Skipped auto-revive, player age ${ageDays} days is under ${MIN_AGE_DAYS} day minimum.`;
                    logToGateway('fail', msg);
                    return;
                }

                revButton.click();
            }, 150);
        };

        if (revButton.classList.contains('disabled') || revButton.classList.contains('cross')) {
            console.log("[UserScript] Revive button is disabled. Watching for it to become active...");
            let disabledTimeout;

            const disabledObserver = new MutationObserver(() => {
                if (!revButton.classList.contains('disabled') && !revButton.classList.contains('cross')) {
                    disabledObserver.disconnect();
                    clearTimeout(disabledTimeout);
                    executeClick();
                }
            });

            disabledObserver.observe(revButton, { attributes: true, attributeFilter: ['class'] });

            disabledTimeout = setTimeout(() => {
                disabledObserver.disconnect();
                logToGateway('fail', '[UserScript] Revive button disabled');
            }, 15000);

            return;
        }

        executeClick();
    };

    const existingButton = document.querySelector('.profile-button-revive');
    if (existingButton) {
        clickReviveButton();
    } else {
        let autoReviveTimeout;

        const targetContainer = document.querySelector('.profile-buttons')
            || document.querySelector('.profile-right-wrapper')
            || document.querySelector('.profile-wrapper')
            || document.getElementById('profileroot')
            || document.body;

        let buttonsListFound = false;

        const autoReviveObserver = new MutationObserver(() => {
            if (buttonsListFound) return;
            const buttonsList = document.querySelector('.buttons-list');
            if (buttonsList) {
                buttonsListFound = true;
                autoReviveObserver.disconnect();
                if (autoReviveTimeout) clearTimeout(autoReviveTimeout);

                const revButton = buttonsList.querySelector('.profile-button-revive');
                if (revButton) {
                    clickReviveButton();
                } else {
                    const specificError = getPlayerStateError();
                    const msg = specificError ? `[UserScript] ${specificError}` : '[UserScript] Target is not in the hospital.';
                    logToGateway('fail', msg);
                }
            }
        });
        autoReviveObserver.observe(targetContainer, { childList: true, subtree: true });

        autoReviveTimeout = setTimeout(() => {
            autoReviveObserver.disconnect();
            const specificError = getPlayerStateError();
            const msg = specificError ? `[UserScript] ${specificError}` : '[UserScript] Auto-revive timed out, revive button not found.';
            logToGateway('fail', msg);
        }, 10000);
    }

    observer.observe(document.getElementById('profileroot') || document.body, { childList: true, subtree: true });
})();
