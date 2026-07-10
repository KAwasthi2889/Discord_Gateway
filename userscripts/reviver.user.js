// ==UserScript==
// @name         Torn Gateway Reviver
// @namespace    http://tampermonkey.net/
// @version      1.5.1
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
    let cbtoken = "";
    let gatewayXid = new URLSearchParams(window.location.search).get("XID");

    let minChanceOverride = 60; // Default, overridden by URL hash from Go backend
    let requiredStatus = null;

    // Replace hash with #auto to prevent accidental re-triggers on manual refresh
    // while keeping a marker so fast_revive knows to stay away
    history.replaceState(null, '', window.location.pathname + window.location.search + '#auto');

    const portMatch = savedHash.match(/cbport=(\d+)/);
    if (portMatch) cbport = parseInt(portMatch[1], 10);

    const tokenMatch = savedHash.match(/token=([a-fA-F0-9]+)/);
    if (tokenMatch) cbtoken = tokenMatch[1];

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
            const url = `http://127.0.0.1:${cbport}/revive?xid=${xid}&status=${status}&reason=${encodeURIComponent(reason)}&token=${cbtoken}&_t=${Date.now()}`;
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

    const isCloudflare = () => document.title.includes('Just a moment') || document.querySelector('#cf-wrapper') || document.querySelector('.cf-browser-verification') || document.querySelector('#challenge-running');

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
        return { chance, text: pageText.trim() };
    }

    function diagnoseAndFail(isTimeout = false) {
        if (!document.querySelector('.main-desc')) {
            if (isCloudflare()) {
                logToGateway('fail', '[CRITICAL] Cloudflare CAPTCHA blocked the request');
            } else {
                logToGateway('fail', '[CRITICAL] Page not loaded');
            }
        } else {
            const specificError = getPlayerStateError();
            if (specificError) {
                logToGateway('fail', `[UserScript] ${specificError}`);
            } else {
                logToGateway('fail', isTimeout ? '[UserScript] Auto-revive timed out, revive button not found.' : '[UserScript] Target is not in the hospital.');
            }
        }
    }

    const watchForSuccessAndClose = (actualChance) => {
        let successFound = false;
        // Phase 3: Watch for success message on .profile-buttons
        const dialogTarget = document.querySelector('.profile-buttons') || document.body;
        let successTimeout;

        const successObserver = new MutationObserver((m, obs) => {
            const responseTextEl = document.querySelector('.profile-buttons-dialog .center-block .text');
            if (responseTextEl) {
                const text = responseTextEl.textContent.trim();
                if (text.includes("chance of success")) return;

                const isSuccess = responseTextEl.classList.contains('t-green') || text.includes('successfully revived');
                const isChanceFailure = text.toLowerCase().includes('attempted to revive') && text.toLowerCase().includes('but failed');

                if (cbport && gatewayXid) {
                    if (isSuccess) {
                        logToGateway('success', '', gatewayXid);
                    } else if (isChanceFailure) {
                        logToGateway('fail', 'failed to revive', gatewayXid);
                    } else {
                        let reason = text;
                        if (text.toLowerCase().includes("enough energy")) {
                            reason = "Not enough energy";
                        }
                        logToGateway('fail', reason, gatewayXid);
                    }
                }
                successFound = true;
                obs.disconnect();
                clearTimeout(successTimeout);
            }
        });

        successObserver.observe(dialogTarget, { childList: true, subtree: true, characterData: true });

        successTimeout = setTimeout(() => {
            successObserver.disconnect();
            if (!successFound) {
                const msg = '[UserScript] Success message not found within 5s.';
                console.log(msg);
                logToGateway('fail', msg);
            }
        }, 5000);
    };



    const getPlayerStateError = () => {
        const descEl = document.querySelector('.main-desc');
        if (!descEl) return null;
        const text = descEl.textContent.trim().toLowerCase();
        if (text.includes("traveling")) return "Not in a hospital, Travelling";
        if (text === "okay") return "User is not in hospital anymore";
        if (text.includes("in hospital for")) return "You are in another country";
        if (text.includes("hospital")) {
            if (text.startsWith("in a ")) return "User is in a different country's hospital";
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

                // PHASE 2: Wait for confirmation dialog
                const profileButtons = document.querySelector('.profile-buttons') || document.body;
                let confirmTimeout;
                const confirmObserver = new MutationObserver(() => {
                    const yesButton = document.querySelector('.confirm-action-yes') || document.querySelector('.confirm-action');
                    if (yesButton) {
                        confirmObserver.disconnect();
                        clearTimeout(confirmTimeout);

                        const dialog = document.querySelector('.profile-buttons-dialog');
                        const reviveInfo = getReviveInfo(dialog || document.body);
                        if (reviveInfo.chance !== null) {
                            if (reviveInfo.chance >= minChanceOverride) {
                                yesButton.click();
                                watchForSuccessAndClose(reviveInfo.chance); // Start Phase 3
                            } else {
                                logToGateway('fail', `[UserScript] Skipped auto-revive, chance ${reviveInfo.chance}% is below minChance ${minChanceOverride}%.`);
                            }
                        } else {
                            logToGateway('fail', `[UserScript] Could not determine success chance. Raw text: "${reviveInfo.text}"`);
                        }
                    }
                });

                confirmObserver.observe(profileButtons, { childList: true, subtree: true });

                confirmTimeout = setTimeout(() => {
                    confirmObserver.disconnect();
                    logToGateway('fail', '[UserScript] Confirmation dialog did not appear.');
                }, 5000);

                revButton.click();
            }, 150);
        };

        if (revButton.classList.contains('cross')) {
            console.log("[UserScript] Target has revives disabled (cross). Watching for it to become active...");
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
                logToGateway('fail', '[UserScript] Target kept revives disabled.');
            }, 15000);

            return;
        } else if (revButton.classList.contains('disabled')) {
            // No cross, just disabled. It means we (the reviver) are restricted.
            logToGateway('fail', '[UserScript] You are in hospital or jail.');
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
                if (autoReviveTimeout) clearInterval(autoReviveTimeout);

                const revButton = buttonsList.querySelector('.profile-button-revive');
                if (revButton) {
                    clickReviveButton();
                } else {
                    diagnoseAndFail(false);
                }
            }
        });
        autoReviveObserver.observe(targetContainer, { childList: true, subtree: true });

        let timeElapsed = 0;
        autoReviveTimeout = setInterval(() => {
            if (isCloudflare()) {
                timeElapsed -= 1000;
                if (timeElapsed < -20000) { // Max 20s for Cloudflare
                    clearInterval(autoReviveTimeout);
                    autoReviveObserver.disconnect();
                    logToGateway('fail', '[CRITICAL] Cloudflare CAPTCHA blocked the request');
                }
                return;
            }

            timeElapsed += 1000;
            if (timeElapsed >= 10000) {
                clearInterval(autoReviveTimeout);
                autoReviveObserver.disconnect();
                diagnoseAndFail(true);
            }
        }, 1000);
    }

})();
