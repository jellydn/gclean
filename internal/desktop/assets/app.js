const token = document.querySelector('meta[name="gclean-token"]').content;
const $ = (id) => document.getElementById(id);
let state = null;
let settings = null;
let action = "trash";
const bytes = (n) => {
	if (!n) return "0 B";
	const u = ["B", "KB", "MB", "GB", "TB"];
	const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), u.length - 1);
	return `${(n / 1024 ** i).toFixed(i ? 1 : 0)} ${u[i]}`;
};
async function api(path, body) {
	const res = await fetch(path, {
		method: body === undefined ? "GET" : "POST",
		headers: {
			"X-Gclean-Token": token,
			...(body === undefined ? {} : { "Content-Type": "application/json" }),
		},
		body: body === undefined ? undefined : JSON.stringify(body),
	});
	const data = await res.json();
	if (!res.ok) throw new Error(data.error || "Request failed");
	return data;
}
function busy(title, copy = "Keep this window open.") {
	$("busy-title").textContent = title;
	$("busy-copy").textContent = copy;
	$("busy").classList.remove("hidden");
}
function idle() {
	$("busy").classList.add("hidden");
}
let toastTimer;
function toast(message, success = false) {
	clearTimeout(toastTimer);
	$("toast").textContent = message;
	$("toast").className = `toast${success ? " success" : ""}`;
	toastTimer = setTimeout(() => $("toast").classList.add("hidden"), 5500);
}
async function load() {
	try {
		[state, settings] = await Promise.all([
			api("/api/state"),
			api("/api/settings"),
		]);
		render();
		renderSettings();
	} catch (e) {
		toast(e.message);
	} finally {
		idle();
	}
}
function lines(value) {
	return value
		.split("\n")
		.map((line) => line.trim())
		.filter(Boolean);
}
function renderSettings() {
	$("keep-contacts").checked = settings.keep.contacts;
	$("keep-replied").checked = settings.keep.replied;
	$("keep-starred").checked = settings.keep.starred;
	$("keep-important").checked = settings.keep.important;
	$("keep-sent").checked = settings.keep.sent_by_user;
	$("recent-days").value = settings.keep.recent_days;
	$("delete-rules").value = settings.delete.join("\n");
	$("archive-rules").value = settings.archive.join("\n");
	$("ignored-domains").value = settings.ignore.join("\n");

	const oauth = settings.oauth;
	const oauthBadge = $("oauth-badge");
	oauthBadge.textContent = oauth.fixtureMode
		? "Fixture mode"
		: oauth.tokenPresent
			? "Connected"
			: oauth.credentialsPresent
				? "Ready to connect"
				: "Setup required";
	oauthBadge.classList.toggle("connected", oauth.tokenPresent);
	$("credential-copy").textContent = oauth.credentialsPresent
		? `Configured securely at ${settings.paths.credentials}`
		: `Choose the downloaded file; it will be stored at ${settings.paths.credentials}`;
	$("settings-connect").disabled =
		oauth.fixtureMode || !oauth.credentialsPresent;
	$("disconnect").disabled = oauth.fixtureMode || !oauth.tokenPresent;
	$("credentials-file").disabled = oauth.fixtureMode;
	$("purge-badge").textContent = oauth.purgeEnabled
		? "Enabled for this session"
		: oauth.permanentDeleteAuthorized
			? "Authorized · restart required"
			: "Disabled";
	$("purge-badge").classList.toggle("connected", oauth.purgeEnabled);
	$("purge-badge").classList.toggle("danger-badge", !oauth.purgeEnabled);

	const labels = {
		config: "Configuration",
		database: "Metadata database",
		undoCache: "Undo cache",
		selection: "TUI selection",
		credentials: "OAuth credentials",
		token: "OAuth token",
	};
	$("settings-paths").innerHTML = Object.entries(settings.paths)
		.map(
			([key, value]) =>
				`<div><dt>${labels[key]}</dt><dd>${escapeHTML(value)}${settings.pathOverrides[key] ? `<small>${escapeHTML(environmentName(key))} override · restart after changing</small>` : ""}</dd></div>`,
		)
		.join("");
}
function environmentName(key) {
	return {
		config: "GCLEAN_CONFIG_PATH",
		database: "GCLEAN_DB_PATH",
		undoCache: "GCLEAN_UNDO_CACHE",
		selection: "GCLEAN_SELECTION_PATH",
		credentials: "GCLEAN_CREDENTIALS_PATH",
		token: "GCLEAN_TOKEN_PATH",
	}[key];
}
function render() {
	const connected = state.authenticated;
	const connection = $("connection");
	connection.classList.toggle("ready", connected);
	connection.querySelector("b").textContent = state.fixtureMode
		? "Safe fixture mode"
		: connected
			? "Gmail connected"
			: "Setup required";
	$("setup").classList.toggle("hidden", connected);
	$("total-storage").textContent = bytes(state.stats.EstimatedStorage);
	$("total-messages").textContent =
		`${state.stats.TotalMessages.toLocaleString()} messages indexed`;
	$("reclaim").textContent = bytes(state.preview.RecoverBytes);
	$("delete-count").textContent =
		`${state.preview.DeleteCount.toLocaleString()} messages in preview`;
	$("keep-count").textContent = state.preview.KeepCount.toLocaleString();
	$("selected-storage").textContent = bytes(state.preview.RecoverBytes);
	$("selected-count").textContent =
		`${state.preview.DeleteCount.toLocaleString()} messages`;
	$("open-trash").disabled = !connected || !state.preview.DeleteCount;
	$("undo-copy").textContent = state.recoveryWarning
		? `Recovery is paused: ${state.recoveryWarning}`
		: state.undoCount
			? `${state.undoCount.toLocaleString()} messages from the last cleanup can be restored.`
			: "No gclean batch is currently waiting in Trash.";
	$("restore").disabled = !connected || !state.undoCount;
	$("open-purge").disabled = !connected || !state.purgeAllowed;
	$("open-purge").title = state.purgeAllowed
		? "Permanently empty all Gmail Trash"
		: "Restart with --allow-purge after an opt-in OAuth login";
	renderSenders();
	renderMessages();
	if (state.auth.state === "error") toast(state.auth.error);
}
function visibleRows() {
	const q = $("filter").value.trim().toLowerCase();
	return state.senders.filter(
		(s) =>
			!q ||
			s.email.toLowerCase().includes(q) ||
			s.reasons.join(" ").toLowerCase().includes(q),
	);
}
function renderSenders() {
	const rows = visibleRows();
	$("sender-count").textContent =
		`${rows.length} sender${rows.length === 1 ? "" : "s"}`;
	$("senders").innerHTML = rows.length
		? rows
				.map(
					(s) =>
						`<tr><td><input class="sender-check" type="checkbox" data-email="${escapeHTML(s.email)}" ${s.checked ? "checked" : ""}></td><td><span class="sender-email">${escapeHTML(s.email)}</span></td><td>${s.reasons.map((r) => `<span class="reason">${escapeHTML(r.replaceAll("_", " "))}</span>`).join(" ")}</td><td>${s.count.toLocaleString()}</td><td>${bytes(s.bytes)}</td></tr>`,
				)
				.join("")
		: '<tr><td colspan="5" class="empty">No candidates match this filter.</td></tr>';
	$("select-all").checked = rows.length > 0 && rows.every((s) => s.checked);
	document.querySelectorAll(".sender-check").forEach((el) => {
		el.addEventListener("change", selectionChanged);
	});
}
function renderMessages() {
	$("messages").innerHTML = state.messages.length
		? state.messages
				.map(
					(m) =>
						`<div class="message"><b>${escapeHTML(m.sender)}</b><span>${escapeHTML(m.subject || "(no subject)")} · ${escapeHTML(m.reason.replaceAll("_", " "))}</span><b>${bytes(m.bytes)}</b></div>`,
				)
				.join("")
		: '<div class="message"><span>No selected cleanup messages.</span></div>';
}
function escapeHTML(v) {
	const d = document.createElement("div");
	d.textContent = v;
	return d.innerHTML;
}
async function selectionChanged(event) {
	const changed = state.senders.find(
		(sender) => sender.email === event.currentTarget.dataset.email,
	);
	if (changed) changed.checked = event.currentTarget.checked;
	const checked = state.senders
		.filter((sender) => sender.checked)
		.map((sender) => sender.email);
	busy("Updating preview…", "No Gmail changes are being made.");
	try {
		state = await api("/api/selection", { senders: checked });
		render();
	} catch (e) {
		toast(e.message);
		await load();
	} finally {
		idle();
	}
}
$("select-all").addEventListener("change", async (e) => {
	for (const row of visibleRows()) row.checked = e.target.checked;
	const checked = state.senders.filter((s) => s.checked).map((s) => s.email);
	busy("Updating preview…");
	try {
		state = await api("/api/selection", { senders: checked });
		render();
	} catch (err) {
		toast(err.message);
	} finally {
		idle();
	}
});
$("filter").addEventListener("input", renderSenders);
$("scan").addEventListener("click", async () => {
	busy(
		"Scanning Gmail metadata…",
		"Large mailboxes can take several minutes. Message bodies are never downloaded.",
	);
	try {
		const result = await api("/api/scan", {});
		toast(result.message, true);
		await load();
	} catch (e) {
		toast(e.message);
	} finally {
		idle();
	}
});
async function connect() {
	busy("Preparing secure OAuth…");
	try {
		const result = await api("/api/login", {});
		const link = $("auth-link");
		link.href = result.authUrl;
		link.classList.remove("hidden");
		window.open(result.authUrl, "_blank", "noopener");
		toast(result.message, true);
		pollAuth();
	} catch (e) {
		toast(e.message);
	} finally {
		idle();
	}
}
$("login").addEventListener("click", connect);
$("settings-connect").addEventListener("click", connect);
async function pollAuth() {
	for (let i = 0; i < 150; i++) {
		await new Promise((r) => setTimeout(r, 2000));
		await load();
		if (state.auth.state !== "waiting") return;
	}
}
$("open-trash").addEventListener("click", () => openDialog("trash"));
$("open-purge").addEventListener("click", () => openDialog("purge"));
function openDialog(kind) {
	action = kind;
	const purge = kind === "purge";
	$("dialog-eyebrow").textContent = purge
		? "IRREVERSIBLE ACTION"
		: "CONFIRM CLEANUP";
	$("dialog-title").textContent = purge
		? "Permanently empty all Gmail Trash?"
		: "Move selected mail to Trash?";
	$("dialog-copy").textContent = purge
		? "This deletes every message currently in Gmail Trash, including items not moved there by gclean. It cannot be undone."
		: "The selected planner-approved messages will move to Gmail Trash. Nothing is permanently deleted, and the last batch can be restored from gclean.";
	$("dialog-size").textContent = purge
		? "Permanent"
		: bytes(state.preview.RecoverBytes);
	$("dialog-count").textContent = purge
		? "All messages in Trash"
		: `${state.preview.DeleteCount.toLocaleString()} selected messages`;
	$("confirmation-label").querySelector("b").textContent = purge
		? "EMPTY TRASH PERMANENTLY"
		: "MOVE TO TRASH";
	$("confirmation").value = "";
	$("confirm-action").textContent = purge
		? "Empty Trash permanently"
		: "Move to Trash";
	$("confirm-action").className = purge ? "danger" : "danger-soft";
	$("confirm-dialog").showModal();
	$("confirmation").focus();
}
$("confirm-action").addEventListener("click", async (e) => {
	e.preventDefault();
	const confirmation = $("confirmation").value;
	const path = action === "purge" ? "/api/purge" : "/api/trash";
	$("confirm-dialog").close();
	busy(
		action === "purge" ? "Emptying Gmail Trash…" : "Moving messages to Trash…",
		action === "purge"
			? "This permanent operation may take several minutes."
			: "An undo record is being saved first.",
	);
	try {
		const result = await api(path, {
			confirmation,
			previewId: state.previewId,
		});
		toast(result.message, true);
		await load();
	} catch (err) {
		toast(err.message);
		await load();
	} finally {
		idle();
	}
});
$("restore").addEventListener("click", async () => {
	if (
		!confirm(`Restore ${state.undoCount} messages from the last gclean batch?`)
	)
		return;
	busy("Restoring messages…", "Reconciling Gmail and the local undo record.");
	try {
		const result = await api("/api/restore", { confirmation: "RESTORE" });
		toast(result.message, true);
		await load();
	} catch (e) {
		toast(e.message);
	} finally {
		idle();
	}
});
$("settings-form").addEventListener("submit", async (event) => {
	event.preventDefault();
	if (!event.currentTarget.reportValidity()) return;
	$("settings-status").textContent = "Saving…";
	try {
		settings = await api("/api/settings", {
			version: settings.version,
			keep: {
				contacts: $("keep-contacts").checked,
				replied: $("keep-replied").checked,
				starred: $("keep-starred").checked,
				important: $("keep-important").checked,
				sent_by_user: $("keep-sent").checked,
				recent_days: Number($("recent-days").value),
			},
			delete: lines($("delete-rules").value),
			archive: lines($("archive-rules").value),
			ignore: lines($("ignored-domains").value),
		});
		$("settings-status").textContent = "Saved · preview refreshed";
		toast("Cleanup settings saved. No Gmail changes were made.", true);
		await load();
	} catch (error) {
		$("settings-status").textContent = "Not saved";
		toast(error.message);
	}
});
$("reset-settings").addEventListener("click", async () => {
	if (
		!confirm("Reset cleanup protections and rules to gclean's safe defaults?")
	)
		return;
	try {
		settings = await api("/api/settings/reset", {});
		renderSettings();
		$("settings-status").textContent = "Defaults restored";
		toast("Safe cleanup defaults restored.", true);
		await load();
	} catch (error) {
		toast(error.message);
	}
});
$("credentials-file").addEventListener("change", async (event) => {
	const file = event.currentTarget.files[0];
	if (!file) return;
	if (
		settings.oauth.credentialsPresent &&
		!confirm(
			"Replace the configured OAuth credentials and disconnect the current session?",
		)
	) {
		event.currentTarget.value = "";
		return;
	}
	busy("Validating OAuth credentials…", "The file stays on this computer.");
	try {
		const credentials = JSON.parse(await file.text());
		const result = await api("/api/settings/credentials", { credentials });
		toast(result.message, true);
		await load();
	} catch (error) {
		toast(
			error instanceof SyntaxError
				? "The selected file is not valid JSON."
				: error.message,
		);
	} finally {
		event.currentTarget.value = "";
		idle();
	}
});
$("disconnect").addEventListener("click", async () => {
	if (!confirm("Disconnect Gmail and remove the local OAuth token?")) return;
	try {
		const result = await api("/api/logout", {});
		toast(result.message, true);
		await load();
	} catch (error) {
		toast(error.message);
	}
});
const navigation = [...document.querySelectorAll("nav a")];
const sections = navigation
	.map((link) => document.querySelector(link.getAttribute("href")))
	.filter(Boolean);
const observer = new IntersectionObserver(
	(entries) => {
		const visible = entries
			.filter((entry) => entry.isIntersecting)
			.sort((a, b) => b.intersectionRatio - a.intersectionRatio)[0];
		if (!visible) return;
		for (const link of navigation) {
			link.classList.toggle(
				"active",
				link.getAttribute("href") === `#${visible.target.id}`,
			);
		}
	},
	{ rootMargin: "-20% 0px -65%", threshold: [0, 0.1] },
);
for (const section of sections) observer.observe(section);
load();
