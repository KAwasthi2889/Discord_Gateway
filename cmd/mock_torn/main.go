package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	http.HandleFunc("/profiles.php", handleProfiles)
	log.Println("Mock Torn server listening on http://127.0.0.1:8080")
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", nil))
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
		
		<button class="profile-button-revive ` + func() string {
		if scenario == "disabled" {
			return "disabled"
		}
		return ""
	}() + `">Revive</button>

		<div id="dialog-container"></div>
		<div class="error-box" id="error-box"></div>

		<script>
			// Mock GM_xmlhttpRequest using standard fetch
			window.GM_xmlhttpRequest = function(details) {
				console.log("Mock GM_xmlhttpRequest intercepting request:", details.url);
				fetch(details.url, { method: details.method || "GET" })
					.then(response => {
						if (details.onload) details.onload({ status: response.status });
					})
					.catch(err => {
						if (details.onerror) details.onerror(err);
					});
			};

			// Simulate the Torn UI clicking logic
			document.querySelector('.profile-button-revive').addEventListener('click', function() {
				console.log("Mock Torn: Revive button clicked");

				if (this.classList.contains('disabled')) {
					return;
				}

				// Remove the button and inject the confirm dialog dynamically
				this.remove();
				
				let chanceText = '100% chance of success';
				if ("` + scenario + `" === "low_chance") {
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
