/* Page compositor.
 *
 * The server sends the edition as one flat run of blocks. This turns that run
 * into A4 sheets: blocks are appended to a sheet's multi-column text block
 * until it overflows, at which point the offending block starts the next
 * sheet. CSS columns do the fine work of flowing copy between columns; this
 * only decides where one page ends and the next begins.
 *
 * The same sheets are what the browser prints, so the screen and the PDF can
 * never disagree about where a page breaks.
 */
(() => {
  const body = document.body;
  if (!body || !body.classList.contains("paper")) return;

  const copy = document.querySelector("[data-copy]");
  const sheets = document.querySelector("[data-sheets]");
  if (!copy || !sheets) return;

  const pager = document.querySelector("[data-pager]");
  const pageLabel = document.querySelector("[data-page-label]");
  const previousButton = document.querySelector("[data-page-prev]");
  const nextButton = document.querySelector("[data-page-next]");
  const viewToggle = document.querySelector("[data-view-toggle]");
  const printButton = document.querySelector("[data-print]");

  // The unset copy, kept aside so the page can be recomposed or handed back to
  // the reading view without a round trip to the server.
  const source = Array.from(copy.children);
  const MAX_SHEETS = 60;
  // The paper's own name, for the running head in each page's bottom margin.
  const masthead = (document.querySelector(".pressroom-mark")?.textContent || "Home News").replace(/\s+/g, " ").trim();

  let frames = [];
  let current = 0;

  const readingPreferred = () => window.matchMedia("(max-width: 700px)").matches;

  /* ------------------------------------------------------------ composition */

  // A group is a run of consecutive blocks that must not be separated: a
  // headline stays with its byline and its photograph. A block marked
  // data-keep-next additionally holds on to whatever follows it, which is what
  // stops a section band being stranded at the foot of a page with its stories
  // overleaf.
  const groupBlocks = (blocks) => {
    const byKey = [];
    for (const block of blocks) {
      const key = block.dataset.group;
      const previous = byKey[byKey.length - 1];
      if (key && previous && previous.key === key) previous.blocks.push(block);
      else byKey.push({ key: key || null, blocks: [block] });
    }
    const groups = [];
    for (const group of byKey) {
      const previous = groups[groups.length - 1];
      const holds = previous && previous.blocks[previous.blocks.length - 1].hasAttribute("data-keep-next");
      if (holds) previous.blocks.push(...group.blocks);
      else groups.push(group);
    }
    return groups;
  };

  // A fixed-height multi-column box pushes surplus copy into columns beyond its
  // own width, so the text block is full the moment its content reaches past
  // its right or bottom edge. Both checks are made against laid-out geometry;
  // the sheet is composed at natural size and only scaled for display
  // afterwards, so the result never depends on the size of the window.
  const overflows = (text) => {
    if (text.scrollWidth > text.clientWidth + 1) return true;
    const last = text.lastElementChild;
    if (!last) return false;
    const box = text.getBoundingClientRect();
    const end = last.getBoundingClientRect();
    return end.right > box.right + 1 || end.bottom > box.bottom + 1;
  };

  const makeSheet = (number) => {
    const frame = document.createElement("div");
    frame.className = "sheet-frame";
    const sheet = document.createElement("section");
    sheet.className = "sheet";
    sheet.dataset.page = String(number);
    sheet.setAttribute("aria-label", `Page ${number}`);
    const text = document.createElement("div");
    text.className = "sheet-body";
    const folio = document.createElement("div");
    folio.className = "sheet-folio";
    const runningHead = document.createElement("span");
    runningHead.textContent = masthead;
    const number_ = document.createElement("strong");
    number_.textContent = String(number);
    folio.append(runningHead, number_);
    sheet.append(text, folio);
    frame.append(sheet);
    sheets.append(frame);
    return { frame, sheet, text, number };
  };

  const lastContentBlock = (text) => {
    for (let i = text.children.length - 1; i >= 0; i--) {
      const child = text.children[i];
      if (!child.classList.contains("jump")) return child;
    }
    return null;
  };

  // Closes a sheet, adding a "continued" line when the article running at the
  // foot of the page carries on overleaf. Adding that line can itself overflow
  // the page, so any blocks it displaces are handed back to the caller to be
  // set at the top of the next sheet.
  const closeSheet = (page, nextGroup) => {
    const carried = [];
    const last = lastContentBlock(page.text);
    const article = last && last.dataset.article;
    const continues = article && nextGroup && nextGroup.blocks[0].dataset.article === article;
    if (!continues) return { carried, jumpFrom: null };

    const jump = document.createElement("p");
    jump.className = "jump";
    jump.textContent = `Continued on page ${page.number + 1}`;
    page.text.append(jump);
    while (overflows(page.text) && page.text.children.length > 2) {
      const displaced = lastContentBlock(page.text);
      if (!displaced) break;
      displaced.remove();
      carried.unshift(displaced);
      page.text.append(jump);
    }
    const stillLast = lastContentBlock(page.text);
    if (!stillLast || stillLast.dataset.article !== article) {
      jump.remove();
      return { carried, jumpFrom: null };
    }
    return {
      carried,
      jumpFrom: { article, from: page.number, headline: headlineFor(article) },
    };
  };

  const headlineFor = (article) => {
    const head = source.find((block) => block.dataset.article === article && block.dataset.headline);
    return head ? head.dataset.headline : "";
  };

  const addJumpFrom = (page, jumpFrom) => {
    const line = document.createElement("p");
    line.className = "jump-from";
    line.textContent = jumpFrom.headline
      ? `${jumpFrom.headline} — continued from page ${jumpFrom.from}`
      : `Continued from page ${jumpFrom.from}`;
    page.text.prepend(line);
  };

  const compose = () => {
    // Sheets must be laid out to be measured, whichever view the reader is in,
    // and the copy must be out of the way; composition is always done at
    // natural size so page breaks do not depend on the viewport.
    body.classList.add("is-composing");
    body.style.setProperty("--sheet-scale", "1");
    sheets.replaceChildren();
    // Single-page mode hides every sheet but the current one, which would make
    // a freshly made sheet unmeasurable. Lay them all out while composing.
    delete sheets.dataset.mode;
    frames = [];
    // Blocks live in exactly one place, so take them back before re-laying out.
    copy.append(...source);

    const queue = groupBlocks(source);
    let page = makeSheet(1);
    let index = 0;

    while (index < queue.length && sheets.children.length <= MAX_SHEETS) {
      const group = queue[index];
      page.text.append(...group.blocks);
      if (!overflows(page.text)) {
        index++;
        continue;
      }
      // A group that cannot fit even on an empty page is left where it is;
      // the sheet clips it rather than looping forever.
      const alone = page.text.children.length === group.blocks.length;
      if (alone) {
        index++;
        page = makeSheet(page.number + 1);
        continue;
      }
      for (const block of group.blocks) block.remove();
      const { carried, jumpFrom } = closeSheet(page, group);
      page = makeSheet(page.number + 1);
      if (jumpFrom) addJumpFrom(page, jumpFrom);
      if (carried.length) {
        queue.splice(index, 0, ...carried.map((block) => ({ key: null, blocks: [block] })));
      }
    }

    frames = Array.from(sheets.children);
    balanceLastSheet();
    numberTheIndex();
    body.setAttribute("data-composed", "");
    body.classList.remove("is-composing");
    fit();
    show(Math.min(current, Math.max(frames.length - 1, 0)));
  };

  // Earlier pages fill column by column, which is how a newspaper reads. The
  // final page is usually part empty, so its copy is spread evenly across the
  // measure instead of being left in a tall stack against the left margin.
  const balanceLastSheet = () => {
    const last = frames[frames.length - 1];
    if (!last || frames.length < 2) return;
    const text = last.querySelector(".sheet-body");
    if (text && !overflows(text)) text.style.columnFill = "balance";
  };

  // Fills the "Inside today" box with the page each section landed on. The
  // slots have a reserved width in CSS, so writing into them cannot reflow
  // page one and make the numbers wrong.
  const numberTheIndex = () => {
    const pageOf = new Map();
    frames.forEach((frame, i) => {
      frame.querySelectorAll("[data-anchor]").forEach((band) => {
        if (!pageOf.has(band.dataset.anchor)) pageOf.set(band.dataset.anchor, i + 1);
      });
    });
    for (const entry of document.querySelectorAll("[data-index-for]")) {
      const slot = entry.querySelector("[data-index-page]");
      if (slot) slot.textContent = pageOf.has(entry.dataset.indexFor) ? String(pageOf.get(entry.dataset.indexFor)) : "—";
    }
  };

  /* ------------------------------------------------------------ presentation */

  // Scales a real A4 sheet down to whatever room the window has.
  const fit = () => {
    if (!frames.length) return;
    // offsetWidth reports the untransformed layout width, so the natural size
    // of a sheet is the same whatever scale is currently applied.
    const natural = frames[0].querySelector(".sheet").offsetWidth;
    const available = sheets.clientWidth - 24;
    const scale = natural > 0 ? Math.min(1, available / natural) : 1;
    body.style.setProperty("--sheet-scale", String(scale > 0 ? scale : 1));
  };

  const show = (i) => {
    if (!frames.length) return;
    current = Math.max(0, Math.min(i, frames.length - 1));
    frames.forEach((frame, index) => {
      if (index === current) frame.setAttribute("data-current", "");
      else frame.removeAttribute("data-current");
    });
    sheets.dataset.mode = "single";
    if (pageLabel) pageLabel.textContent = `Page ${current + 1} of ${frames.length}`;
    if (previousButton) previousButton.disabled = current === 0;
    if (nextButton) nextButton.disabled = current === frames.length - 1;
    if (pager) pager.hidden = frames.length < 2;
  };

  const setView = (view) => {
    body.dataset.view = view;
    if (viewToggle) {
      viewToggle.setAttribute("aria-pressed", view === "reading" ? "true" : "false");
      viewToggle.textContent = view === "reading" ? "Page view" : "Reading view";
    }
    if (view === "pages") {
      compose();
    } else {
      body.removeAttribute("data-composed");
      body.classList.remove("is-composing");
      copy.append(...source);
      sheets.replaceChildren();
      frames = [];
      if (pager) pager.hidden = true;
    }
  };

  /* ------------------------------------------------------------------ events */

  previousButton?.addEventListener("click", () => show(current - 1));
  nextButton?.addEventListener("click", () => show(current + 1));
  viewToggle?.addEventListener("click", () => setView(body.dataset.view === "reading" ? "pages" : "reading"));
  printButton?.addEventListener("click", () => window.print());

  document.addEventListener("keydown", (event) => {
    if (body.dataset.view !== "pages" || !frames.length) return;
    const target = event.target;
    if (target instanceof HTMLElement && /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName)) return;
    if (event.key === "ArrowRight" || event.key === "PageDown") { show(current + 1); event.preventDefault(); }
    else if (event.key === "ArrowLeft" || event.key === "PageUp") { show(current - 1); event.preventDefault(); }
    else if (event.key === "Home") { show(0); event.preventDefault(); }
    else if (event.key === "End") { show(frames.length - 1); event.preventDefault(); }
  });

  let resizeTimer = 0;
  window.addEventListener("resize", () => {
    window.clearTimeout(resizeTimer);
    // Sheets are measured in millimetres, so a resize only changes the scale
    // the screen draws them at, never where the pages break.
    resizeTimer = window.setTimeout(fit, 120);
  });

  // Printing must never produce something the reader has not seen, so compose
  // first if the reader is in the scrolling view.
  window.addEventListener("beforeprint", () => {
    if (!body.hasAttribute("data-composed")) compose();
  });

  setView(readingPreferred() ? "reading" : "pages");
  if (document.fonts && document.fonts.ready) {
    document.fonts.ready.then(() => {
      if (body.dataset.view === "pages") compose();
    });
  }
})();
