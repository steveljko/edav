// Enhancements layered on top of pages that already work without them. Each
// one is additive: the markup it enhances is functional on its own, and the
// controls that only make sense with scripting stay hidden until this runs.
(function () {
	"use strict";

	document.documentElement.classList.add("has-js");

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

	// Filter a list as you type. Rows carry their own search text so the
	// filter never has to guess which parts of the markup are meaningful.
	document.addEventListener("input", function (event) {
		var input = event.target.closest("[data-filter]");
		if (!input) return;

		var list = document.getElementById(input.getAttribute("data-filter"));
		if (!list) return;

		var needle = input.value.trim().toLowerCase();
		var shown = 0;

		list.querySelectorAll("[data-search]").forEach(function (row) {
			var match = !needle || row.getAttribute("data-search").indexOf(needle) !== -1;
			row.hidden = !match;
			if (match) shown++;
		});

		var empty = list.querySelector("[data-filter-empty]");
		if (empty) empty.hidden = shown !== 0;
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
