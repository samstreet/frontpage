(() => {
  const chat = document.querySelector("[data-chat]");
  if (!chat) return;

  const form = chat.querySelector("[data-chat-form]");
  const input = form.querySelector("textarea[name=message]");
  const messages = chat.querySelector("[data-chat-messages]");
  const feedback = chat.querySelector("[data-chat-feedback]");
  const submit = form.querySelector("button[type=submit]");
  const clear = chat.querySelector("[data-clear-chat]");
  const base = chat.dataset.endpoint;
  const deleteBase = chat.dataset.deleteEndpoint;
  let conversationID = chat.dataset.conversationId || "";

  const addMessage = (role, text, sources = []) => {
    const article = document.createElement("article");
    article.className = `message ${role === "user" ? "user-message" : "assistant-message"}`;
    const label = document.createElement("span");
    label.className = "message-label";
    label.textContent = role === "user" ? "You" : "Home News";
    const paragraph = document.createElement("p");
    paragraph.textContent = text;
    article.append(label, paragraph);

    if (sources.length) {
      const list = document.createElement("ul");
      list.className = "citation-list";
      list.setAttribute("aria-label", "Sources cited");
      for (const source of sources) {
        if (!source || !source.url || !source.headline) continue;
        const item = document.createElement("li");
        const link = document.createElement("a");
        link.href = source.url;
        link.target = "_blank";
        link.rel = "noopener noreferrer";
        link.textContent = source.headline;
        const attribution = document.createElement("span");
        attribution.textContent = ` · ${source.source_name || "Source"} ↗`;
        link.append(attribution);
        item.append(link);
        list.append(item);
      }
      if (list.childElementCount) article.append(list);
    }
    messages.append(article);
    article.scrollIntoView({ block: "nearest", behavior: "smooth" });
  };

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const message = input.value.trim();
    if (!message || submit.disabled) return;

    addMessage("user", message);
    input.value = "";
    feedback.textContent = "Searching saved stories…";
    submit.disabled = true;
    input.disabled = true;
    try {
      const response = await fetch(base, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ message, ...(conversationID ? { conversation_id: conversationID } : {}) }),
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || "The answer could not be generated. Please try again.");
      conversationID = result.conversation_id || conversationID;
      addMessage("assistant", result.answer || "No answer was returned.", result.sources || []);
      feedback.textContent = "";
    } catch (error) {
      feedback.textContent = error.message || "Could not reach Home News. Your question is still available to retry.";
      input.value = message;
      input.focus();
    } finally {
      submit.disabled = false;
      input.disabled = false;
    }
  });

  clear.addEventListener("click", async () => {
    if (!conversationID) {
      messages.replaceChildren();
      conversationID = "";
      feedback.textContent = "Conversation cleared.";
      return;
    }
    clear.disabled = true;
    feedback.textContent = "Clearing conversation…";
    try {
      const response = await fetch(`${deleteBase}${encodeURIComponent(conversationID)}`, { method: "DELETE" });
      if (!response.ok) throw new Error("The conversation could not be cleared. Please try again.");
      conversationID = "";
      messages.replaceChildren();
      addMessage("assistant", "What would you like to know about the stories in your archive?");
      feedback.textContent = "Conversation cleared.";
    } catch (error) {
      feedback.textContent = error.message;
    } finally {
      clear.disabled = false;
    }
  });
})();
