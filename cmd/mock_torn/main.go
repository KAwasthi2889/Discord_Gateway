package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/log", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Printf("BROWSER_LOG: %s\n", string(body))
		w.WriteHeader(200)
	})
	mux.HandleFunc("/profiles.php", handleProfiles)

	log.Println("Mock Torn server listening on http://127.0.0.1:58080")
	log.Fatal(http.ListenAndServe("127.0.0.1:58080", mux))
}

func handleProfiles(w http.ResponseWriter, r *http.Request) {
	scenario := r.URL.Query().Get("scenario")
	// Read the actual userscript from disk
	scriptBytes, err := os.ReadFile("userscripts/reviver.user.js")
	if err != nil {
		// Fallback to absolute path or other relative path if running from subfolder
		scriptBytes, err = os.ReadFile("../../userscripts/reviver.user.js")
		if err != nil {
			http.Error(w, "Could not find reviver.user.js: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	scriptContent := string(scriptBytes)

	// Inject the mock HTML.
	// This HTML simulates the structure of a Torn profile page with a Revive button.
	// It includes a polyfill for GM_xmlhttpRequest so the userscript can make requests natively.
	html := `
	<!DOCTYPE html>
	<html lang="en">
	<head>
		<meta charset="UTF-8">
		<title>Mock Torn Profile</title>
		<style>
			body { font-family: sans-serif; background: #333; color: white; padding: 20px; }
			.profile-button-revive { padding: 10px 20px; background: red; color: white; cursor: pointer; }
			.profile-button-revive.disabled { background: grey; cursor: not-allowed; }
			.confirm-action-yes { padding: 10px 20px; background: green; color: white; cursor: pointer; margin-top: 10px; }
			.profile-buttons-dialog { display: none; margin-top: 20px; border: 1px solid #555; padding: 10px; }
			.t-green { color: lightgreen; }
			.error-box { background: darkred; color: white; padding: 10px; margin-top: 10px; border: 1px solid red; display: none; }
		</style>
	</head>
	<body>
		<h1>Mock Torn Profile</h1>
		<div class="user-info-list-wrap"></div>
		<div class="profile-wrapper">
			<ul class="big row basic-info svg">
				<li id="icon2-profile-12345" class="user-status-16-` + func() string {
		if scenario == "status_offline" {
			return "offline"
		} else if scenario == "status_away" {
			return "away"
		}
		return "online"
	}() + ` left" data-is-tooltip-opened="false"></li>
			</ul>
		</div>
		
		` + func() string {
		if scenario == "traveling_hospital" || scenario == "traveling_country" || scenario == "okay" || scenario == "unfamiliar" || scenario == "hang" {
			return ""
		}
		btnClass := "profile-button-revive "
		if scenario == "disabled" {
			btnClass += "disabled"
		}
		return `<button class="` + btnClass + `">Revive</button>`
	}() + `

	<div class="main-desc">` + func() string {
		if scenario == "traveling_hospital" {
			return "In British hospital"
		} else if scenario == "traveling_country" {
			return "In Switzerland"
		} else if scenario == "okay" {
			return "Okay"
		} else if scenario == "unfamiliar" {
			return "Some weird unknown Torn error text"
		}
		return ""
	}() + `</div>

		<div id="dialog-container"></div>
		<div class="error-box" id="error-box"></div>

		<script>
			console.log("Mock Torn: Script initializing");
			const _originalLog = console.log;
			console.log = function(...args) {
				_originalLog(...args);
				fetch('/log', { method: 'POST', body: args.join(' ') }).catch(e => {});
			};
			const _originalError = console.error;
			console.error = function(...args) {
				_originalError(...args);
				fetch('/log', { method: 'POST', body: 'ERROR: ' + args.join(' ') }).catch(e => {});
			};
			window.onerror = function(msg, url, line) {
				fetch('/log', { method: 'POST', body: 'UNCAUGHT ERROR: ' + msg + ' at line ' + line }).catch(e => {});
			};

			// Mock GM_xmlhttpRequest using standard fetch
			const GM_xmlhttpRequest = function(details) {
				console.log("Mock GM_xmlhttpRequest intercepting request:", details.url);
				const fetchOpts = { method: details.method || "GET" };
				if (details.data) {
					fetchOpts.body = details.data;
				}
				fetch(details.url, fetchOpts)
					.then(response => response.text().then(text => {
						if (details.onload) details.onload({ status: response.status, responseText: text });
					}))
					.catch(err => {
						if (details.onerror) details.onerror(err);
					});
			};

			// Simulate the Torn UI clicking logic
			const reviveBtn = document.querySelector('.profile-button-revive');
			if (reviveBtn) {
				reviveBtn.addEventListener('click', function() {
				console.log("Mock Torn: Revive button clicked");

				if (this.classList.contains('disabled')) {
					return;
				}

				// Remove the button and inject the confirm dialog dynamically
				this.remove();
				
				let chanceText = '100% chance of success';
				if ("` + scenario + `" === "low_chance" || "` + scenario + `" === "low_chance_fail") {
					chanceText = '10% chance of success';
				}

				document.getElementById('dialog-container').innerHTML =
					'<div class="confirm-dialog">' +
						'<p>' + chanceText + '</p>' +
						'<button class="confirm-action-yes">Yes</button>' +
					'</div>';
				
				document.querySelector('.confirm-action-yes').addEventListener('click', function() {
					console.log("Mock Torn: Confirm Yes clicked");
					document.querySelector('.confirm-dialog').remove();
					
					if ("` + scenario + `" === "timeout") {
						return; // Do nothing, simulate timeout
					}

					if ("` + scenario + `" === "energy_error") {
						setTimeout(() => {
							document.getElementById('dialog-container').innerHTML = 
								'<div class="profile-buttons-dialog" style="display:block;">' +
									'<div class="center-block">' +
										'<div class="text">You do not have enough energy to perform this action.</div>' +
									'</div>' +
								'</div>';
						}, 500);
						return;
					}

				if ("` + scenario + `" === "chance_fail" || "` + scenario + `" === "low_chance_fail") {
					setTimeout(() => {
						document.getElementById('dialog-container').innerHTML = 
							'<div class="profile-buttons-dialog" style="display:block;">' +
								'<div class="center-block">' +
									'<div class="text t-red">You attempted to revive ThePlayer but failed</div>' +
								'</div>' +
							'</div>';
					}, 500);
					return;
				}

					// Simulate network delay for revive
					setTimeout(() => {
						document.getElementById('dialog-container').innerHTML = 
							'<div class="profile-buttons-dialog" style="display:block;">' +
								'<div class="center-block">' +
									'<div class="text t-green">You successfully revived the player!</div>' +
								'</div>' +
							'</div>';
					}, 500);
				});
			});
			}
		</script>
		
		<script>
			// ==========================================
			// INJECTED PRODUCTION USERSCRIPT BELOW
			// ==========================================
			` + scriptContent + `
		</script>
	</body>
	</html>
	`

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, strings.NewReader(html))
}
