// ==UserScript==
// @name         Torn Fast Revive Manual
// @namespace    http://tampermonkey.net/
// @version      3.7.1
// @description  Manual Revives based on success chance and player status. Supports hospital view auto-confirm.
// @author       Ever2889 [4040971]
// @match        https://www.torn.com/profiles.php*
// @match        https://www.torn.com/hospitalview.php*
// @icon         https://www.google.com/s2/favicons?sz=64&domain=torn.com
// @license      MIT
// @run-at       document-idle
// ==/UserScript==

(function () {
    'use strict';

    if (window.location.hash.includes('autorevive')) {
        return; // Let reviver.user.js handle gateway tabs
    }

    let isConfirming = false;
    const resetConfirming = () => { isConfirming = false; };

    // Default settings with safe parsing
    let settings = {
        threshold: 60,
        blockEarlyDischarge: true
    };
    try {
        const stored = localStorage.getItem('fastReviveSettings');
        if (stored) {
            settings = JSON.parse(stored);
        }
    } catch (e) {
        console.warn('[FastRevive] Corrupted settings found in localStorage. Reverting to defaults.', e);
        localStorage.setItem('fastReviveSettings', JSON.stringify(settings));
    }

    function saveSettings() {
        localStorage.setItem('fastReviveSettings', JSON.stringify(settings));
    }

    const isHospital = window.location.href.includes("hospitalview.php");

    function getReviveInfo(container = document.body) {
        let pageText = "";
        const confirmDialog = container.querySelector('.confirm-revive');

        if (confirmDialog) {
            pageText = confirmDialog.innerText || confirmDialog.textContent;
        } else {
            const textEl = container.querySelector('.profile-buttons-dialog .text') || container.querySelector('div.text');
            if (textEl) {
                pageText = textEl.textContent || textEl.innerText;
            } else {
                pageText = container.innerText || container.textContent;
            }
        }

        const match = pageText.match(/(\d+(?:\.\d+)?)% chance of success/);
        const chance = match ? parseFloat(match[1]) : null;
        const isEarlyDischarge = pageText.includes("Early Discharge");

        return { chance, isEarlyDischarge };
    }

    function autoConfirmRevive() {
        if (isConfirming) return;

        if (isHospital) {
            const listItems = document.querySelectorAll('.user-info-list-wrap li');
            for (const li of listItems) {
                const confirmRevive = li.querySelector('.confirm-revive');
                if (confirmRevive && getComputedStyle(confirmRevive).display !== 'none') {
                    const yesButton = li.querySelector('.action-yes');
                    if (yesButton) {
                        const reviveInfo = getReviveInfo(li);
                        if (reviveInfo.chance !== null && reviveInfo.chance >= settings.threshold) {
                            if (settings.blockEarlyDischarge && reviveInfo.isEarlyDischarge) {
                                continue;
                            }

                            isConfirming = true;
                            yesButton.click();

                            setTimeout(resetConfirming, 500);
                            return;
                        }
                    }
                }
            }
        } else {
            const yesButton = document.querySelector('.confirm-action-yes') || document.querySelector('.confirm-action');
            if (yesButton) {
                const dialog = document.querySelector('.profile-buttons-dialog');
                const reviveInfo = getReviveInfo(dialog || document.body);
                if (reviveInfo.chance !== null) {
                    if (reviveInfo.chance >= settings.threshold) {
                        isConfirming = true;
                        yesButton.click();
                        setTimeout(resetConfirming, 500);
                    }
                }
            }
        }
    }

    function createSettingsUI() {
        if (isHospital) {
            const waitForElm = (selector) => {
                return new Promise(resolve => {
                    if (document.querySelector(selector)) return resolve(document.querySelector(selector));
                    const observer = new MutationObserver(mutations => {
                        if (document.querySelector(selector)) {
                            observer.disconnect();
                            resolve(document.querySelector(selector));
                        }
                    });
                    observer.observe(document.body, { childList: true, subtree: true });
                });
            };

            waitForElm('.msg-info-wrap').then((msgItemWrap) => {
                if (msgItemWrap.querySelector('.fast-revives-container')) return;

                const container = document.createElement('div');
                container.classList.add('fast-revives-container');
                container.style.display = 'flex';
                container.style.alignItems = 'center';
                container.style.gap = '15px';
                container.style.marginTop = '10px';

                const p = document.createElement('p');
                p.textContent = `Set FastRevive Threshold (${settings.threshold}%)`;
                p.style.cursor = 'pointer';
                p.style.color = 'var(--default-blue-color)';
                p.style.fontWeight = 'bold';

                p.addEventListener('click', () => {
                    const newThreshold = prompt('Enter the success threshold (as a percentage):', settings.threshold);
                    if (newThreshold !== null) {
                        const parsedValue = parseFloat(newThreshold);
                        if (!isNaN(parsedValue) && parsedValue >= 0 && parsedValue <= 100) {
                            settings.threshold = parsedValue;
                            saveSettings();
                            p.textContent = `Set FastRevive Threshold (${settings.threshold}%)`;
                            alert(`Success threshold updated to ${settings.threshold}%`);
                        } else {
                            alert('Invalid input. Please enter a number between 0 and 100.');
                        }
                    }
                });

                const toggleText = document.createElement('p');
                toggleText.textContent = `Block Early Discharge: ${settings.blockEarlyDischarge ? 'ON' : 'OFF'}`;
                toggleText.style.cursor = 'pointer';
                toggleText.style.color = 'var(--default-blue-color)';
                toggleText.style.fontWeight = 'bold';

                toggleText.addEventListener('click', () => {
                    settings.blockEarlyDischarge = !settings.blockEarlyDischarge;
                    saveSettings();
                    toggleText.textContent = `Block Early Discharge: ${settings.blockEarlyDischarge ? 'ON' : 'OFF'}`;
                });

                container.appendChild(p);
                container.appendChild(toggleText);
                msgItemWrap.appendChild(container);

                const msgObserver = new MutationObserver(() => {
                    if (!msgItemWrap.querySelector('.fast-revives-container')) {
                        msgItemWrap.appendChild(container);
                    }
                });
                msgObserver.observe(msgItemWrap, { childList: true, subtree: true });
            });

        } else {
            const actionsText = document.querySelector('.title-black');
            if (actionsText) {
                const button = document.createElement('button');
                button.textContent = `Set Revive Threshold (${settings.threshold}%)`;
                button.style.marginLeft = '10px';
                button.style.cursor = 'pointer';
                button.style.color = '#FF0000';

                button.addEventListener('click', () => {
                    const newThreshold = prompt('Enter the success threshold (as a percentage):', settings.threshold);
                    if (newThreshold !== null) {
                        const parsedValue = parseFloat(newThreshold);
                        if (!isNaN(parsedValue) && parsedValue >= 0 && parsedValue <= 100) {
                            settings.threshold = parsedValue;
                            saveSettings();
                            button.textContent = `Set Revive Threshold (${settings.threshold}%)`;
                            alert(`Success threshold updated to ${settings.threshold}%`);
                        } else {
                            alert('Invalid input. Please enter a number between 0 and 100.');
                        }
                    }
                });
                actionsText.parentNode.insertBefore(button, actionsText.nextSibling);
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

    if (!isHospital) {
        document.addEventListener('keydown', (event) => {
            const active = document.activeElement;
            const isTyping = active && (
                active.tagName === 'INPUT' ||
                active.tagName === 'TEXTAREA' ||
                active.isContentEditable
            );
            if (isTyping) return;

            if (event.key.toLowerCase() === 'r' && !event.repeat) {
                const revButton = document.querySelector('.profile-button-revive');
                if (revButton) {
                    revButton.click();
                }
            }
        });
    }

    const rootTarget = isHospital ? document.querySelector('.user-info-list-wrap') : document.getElementById('profileroot');
    const safeTarget = rootTarget || document.body;
    observer.observe(safeTarget, { childList: true, subtree: true });

    createSettingsUI();
})();
