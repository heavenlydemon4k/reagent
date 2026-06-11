# Product Vision

## The Problem
Email inboxes are passive, noisy, and demand constant scrolling. Users lose context switching between chat apps and email clients. AI assistants that only draft replies miss the bigger picture: the inbox is a workflow, not a document store.

## The Solution
Reagent turns the inbox into a conversation. The agent is a persistent teammate that:
1. **Handles the noise** — organizes, labels, archives routine mail.
2. **Surfaces the signal** — presents critical decisions as structured cards at the right time.
3. **Stays present** — always in the chat, ready to answer questions, draft, or investigate.
4. **Shows its work** — every claim is verifiable via source email fetch.
5. **Respects the user** — nothing sends without preview and approval.

## UX Principles

### 1. Chat is the Default
The user opens the app and sees the chat. The input bar is always there. There is no "switch to chat mode" — chat *is* the mode.

### 2. Cards are Messages
Decision cards are not modals or popups. They are rich message bubbles in the chat stream. Each card presents context and a single question. The user replies in the chat input — their text or voice response is the decision. No button arrays. Resolved cards collapse or gray out.

### 3. Sessions are Real
A decision stack session is a real chat session. It has a title, start time, and history. The user can say "pause" and resume later. The agent remembers where they left off.

### 4. Source is One Click Away
Every AI message that references an email has a `[Source]` chip. Clicking it expands the original email (subject, from, body) inline below the message. No context switching.

### 5. Preview is Mandatory
Before any email is sent on the user's behalf, a preview card appears. The user sees exactly what will be sent, who it goes to, and the source email that triggered it. Send is explicit.

### 6. Inbox is Secondary
The traditional inbox view exists but is secondary. It's for browsing, searching, and dragging emails into the chat. The agent's organizational actions are visible here as labels/badges.

## User Flows

### Morning Stack
1. User opens app at 9am.
2. Batch gate shows: "4 decisions · ~8 min · Start Clearing?"
3. User taps Start.
4. Card 1 appears with context and a question. User types or speaks their decision.
5. Draft preview appears. User approves. Send.
6. Card 2 appears automatically. Continue until stack is empty or user exits.

### Ad-hoc Query
1. User is in chat: "Did anyone reply about the server migration?"
2. Agent searches KB, finds relevant thread: "Yes, DevOps confirmed the migration is scheduled for Thursday. [Source]"
3. User clicks [Source], sees original email inline.
4. User: "Draft a thank-you reply." Agent generates preview. User approves. Send.

### Autonomous Handling
1. Newsletter arrives.
2. Classification: `auto`.
3. Agent reads, extracts key info, archives, adds label "Newsletter/Q3".
4. User later asks: "What was in the newsletter today?" Agent summarizes from KB.

### Inbox Drag
1. User switches to inbox view.
2. Sees an email from an unknown sender.
3. Drags email into chat.
4. Agent analyzes: "This looks like a sales pitch. Want me to draft a polite decline?"
5. User: "Yes." Preview → Approve → Send.
