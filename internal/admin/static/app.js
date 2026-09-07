// Enhancements layered on top of pages that already work without them. Each
// one is additive: the markup it enhances is functional on its own, and the
// controls that only make sense with scripting stay hidden until this runs.
(function () {
	"use strict";

	var root = document.documentElement;
	root.classList.add("has-js");

	// Theme. The stored choice was already applied by a small script in the
	// head, before the first paint; this only keeps the control in step with
	// it and records a change. "system" is the absence of a choice, so it is
	// stored as a removal rather than as a third value in the markup.
	var THEME_KEY = "edav-theme";

	function storedTheme() {
		try {
			var t = localStorage.getItem(THEME_KEY);
			return t === "dark" || t === "light" ? t : "system";
		} catch (e) {
			return "system";
		}
	}

	function showTheme(theme) {
		document.querySelectorAll("[data-theme-set]").forEach(function (button) {
			var on = button.getAttribute("data-theme-set") === theme;
			button.setAttribute("aria-pressed", on ? "true" : "false");
		});
	}

	showTheme(storedTheme());

	document.addEventListener("click", function (event) {
		var button = event.target.closest("[data-theme-set]");
		if (!button) return;

		var theme = button.getAttribute("data-theme-set");
		if (theme === "system") {
			root.removeAttribute("data-theme");
		} else {
			root.setAttribute("data-theme", theme);
		}

		try {
			if (theme === "system") localStorage.removeItem(THEME_KEY);
			else localStorage.setItem(THEME_KEY, theme);
		} catch (e) {}

		showTheme(theme);
	});

	// Colour transitions are enabled only after the first paint, so switching
	// the theme animates while loading a page does not.
	window.requestAnimationFrame(function () {
		window.requestAnimationFrame(function () { root.classList.add("theme-ready"); });
	});

	// A confirmation the page draws itself, rather than the browser's dialog,
	// which cannot say what is about to be deleted in the interface's own voice.
	// The button carries the wording; without scripting it is a link to a
	// confirmation page instead, so nothing here is the only way through.
	var modal = document.getElementById("confirm-modal");

	document.addEventListener("click", function (event) {
		var button = event.target.closest("[data-confirm]");
		if (!button || !modal || typeof modal.showModal !== "function") return;

		event.preventDefault();

		var form = button.closest("form");
		if (!form) return;

		modal.querySelector("[data-modal-title]").textContent =
			button.getAttribute("data-confirm-title") || "Are you sure?";
		modal.querySelector("[data-modal-body]").textContent =
			button.getAttribute("data-confirm") || "";
		modal.querySelector("[data-modal-verb]").textContent =
			button.getAttribute("data-confirm-verb") || "Delete";

		modal.returnValue = "cancel";
		modal.showModal();

		modal.addEventListener("close", function once() {
			modal.removeEventListener("close", once);
			if (modal.returnValue === "confirm") form.submit();
		});
	});

	// Clicking the backdrop dismisses it, which is what people expect of a
	// dialog that is asking rather than telling.
	if (modal) {
		modal.addEventListener("click", function (event) {
			if (event.target === modal) modal.close("cancel");
		});
	}

	// Forms that would otherwise sit at the bottom of a page open in a dialog
	// instead. Without scripting the dialog is styled as an ordinary section,
	// so the form is still reachable.
	document.addEventListener("click", function (event) {
		var open = event.target.closest("[data-dialog]");
		if (open) {
			var target = document.getElementById(open.getAttribute("data-dialog"));
			if (target && typeof target.showModal === "function") {
				event.preventDefault();
				target.showModal();
				var first = target.querySelector("input:not([type=hidden])");
				if (first) first.focus();
			}
			return;
		}
		if (event.target.closest("[data-dialog-close]")) {
			var owner = event.target.closest("dialog");
			if (owner) owner.close("cancel");
		}
	});

	// A form the server rejected comes back with the dialog marked open, so the
	// message lands where the fields are rather than behind the backdrop.
	document.querySelectorAll("dialog[data-open]").forEach(function (d) {
		if (typeof d.showModal === "function") d.showModal();
	});

	document.querySelectorAll("dialog.modal-form").forEach(function (d) {
		d.addEventListener("click", function (event) {
			if (event.target === d) d.close("cancel");
		});
	});

	// Derive a URL slug from a name as it is typed, and stop as soon as the
	// slug is edited by hand. The server derives the same slug when the field
	// is left empty, so this only shows what is about to happen.
	document.addEventListener("input", function (event) {
		var source = event.target.closest("[data-slug-source]");
		if (!source) return;

		var target = document.getElementById(source.getAttribute("data-slug-source"));
		if (!target || target.dataset.touched === "true") return;

		target.value = slugify(source.value);
	});

    document.addEventListener("input", function (event) {
		var target = event.target.closest("[data-slug]");
		if (target) target.dataset.touched = target.value === "" ? "false" : "true";
	});

	function slugify(name) {
		var out = "";
		var dash = false;

		name.toLowerCase().trim().split("").forEach(function (ch) {
			if (/[a-z0-9._]/.test(ch)) {
				out += ch;
				dash = false;
			} else if (!dash && out.length > 0) {
				out += "-";
				dash = true;
			}
		});
		return out.replace(/^[-._]+|[-._]+$/g, "").slice(0, 60).replace(/[-._]+$/, "");
	}

	// Copy a value to the clipboard. Client setup is mostly a page of URLs to
	// paste elsewhere, and selecting one by hand is the fiddliest part of it.
	document.addEventListener("click", function (event) {
		var button = event.target.closest("[data-copy]");
		if (!button) return;

		var source = document.getElementById(button.getAttribute("data-copy"));
		if (!source) return;

		var text = (source.textContent || "").trim();
		var done = function () {
			var original = button.getAttribute("data-label") || button.textContent;
			button.setAttribute("data-label", original);
			button.textContent = "Copied";
			button.classList.add("copied");
			window.setTimeout(function () {
				button.textContent = original;
				button.classList.remove("copied");
			}, 1400);
		};

		if (navigator.clipboard && window.isSecureContext) {
			navigator.clipboard.writeText(text).then(done, selectInstead);
		} else {
			selectInstead();
		}

		// Without clipboard access, selecting the text at least saves the
		// careful dragging.
		function selectInstead() {
			var range = document.createRange();
			range.selectNodeContents(source);
			var selection = window.getSelection();
			selection.removeAllRanges();
			selection.addRange(range);
		}
	});

	// Add another phone or email row. The server already accepts as many as
	// the form submits; without this you get one blank row per save.
	document.addEventListener("click", function (event) {
		var button = event.target.closest("[data-add-row]");
		if (!button) return;
		event.preventDefault();

		var group = document.getElementById(button.getAttribute("data-add-row"));
		if (!group) return;

		var rows = group.querySelectorAll(".repeat-row");
		var last = rows[rows.length - 1];
		if (!last) return;

		var copy = last.cloneNode(true);
		copy.querySelectorAll("input").forEach(function (input) {
			input.value = "";
			input.removeAttribute("id");
		});
		copy.querySelectorAll("label").forEach(function (label) {
			label.removeAttribute("for");
		});
		group.appendChild(copy);

		var input = copy.querySelector("input");
		if (input) input.focus();
	});

	// An all-day event has no times to fill in, so the time fields go away
	// rather than sitting there ignored. Without scripting they stay visible
	// and are simply not read.
	document.addEventListener("change", function (event) {
		var toggle = event.target.closest("[data-toggle]");
		if (!toggle) return;

		document.querySelectorAll("." + toggle.getAttribute("data-toggle")).forEach(function (field) {
			field.classList.toggle("is-hidden", toggle.checked);
		});
	});

	// Keep the end from preceding the start while a date is being picked.
	document.addEventListener("change", function (event) {
		if (event.target.id !== "start_date") return;

		var end = document.getElementById("end_date");
		if (end && end.value && end.value < event.target.value) end.value = event.target.value;
	});

	// Remove a row outright, rather than clearing it and remembering that an
	// empty value means removal.
	document.addEventListener("click", function (event) {
		var button = event.target.closest("[data-remove-row]");
		if (!button) return;
		event.preventDefault();

		var row = button.closest(".repeat-row");
		if (!row) return;

		var group = row.parentElement;
		if (group.querySelectorAll(".repeat-row").length > 1) {
			row.remove();
			return;
		}
		// Never leave the group with nothing to type into.
		row.querySelectorAll("input").forEach(function (input) { input.value = ""; });
	});
})();
