// Decision Stack — Quick Reply Chips
//
// Horizontal scrollable chips that provide context-aware templates
// based on card type (pricing, meeting, vendor, etc.).
// Tapping a chip fills the input field with the template text.

import React, { useMemo } from "react";
import {
  View,
  Text,
  TouchableOpacity,
  ScrollView,
  StyleSheet,
} from "react-native";
import { Colors, Type, Space } from "../../styles/cardStyles";

// ─── Quick Reply Templates ─────────────────────────────────────────────────

export type CardCategory =
  | "pricing"
  | "meeting"
  | "vendor"
  | "request"
  | "introduction"
  | "follow_up"
  | "generic";

interface QuickReplyTemplate {
  label: string;
  template: string;
}

const REPLY_TEMPLATES: Record<CardCategory, QuickReplyTemplate[]> = {
  pricing: [
    { label: "Approve", template: "Approve at the quoted price" },
    { label: "Counter", template: "Counter with " },
    { label: "Decline", template: "Decline — budget constraints" },
    { label: "Ask details", template: "Ask for more pricing details" },
    { label: "Hold", template: "Hold — need internal approval" },
  ],
  meeting: [
    { label: "Accept", template: "Accept the meeting" },
    { label: "Decline", template: "Decline — send regrets" },
    { label: "Reschedule", template: "Suggest alternative time: " },
    { label: "Shorten", template: "Ask for 15min instead" },
    { label: "Delegate", template: "Forward to " },
  ],
  vendor: [
    { label: "Standard terms", template: "Approve with standard terms" },
    { label: "Push back", template: "Push back on deliverables" },
    { label: "Negotiate", template: "Negotiate better terms" },
    { label: "Legal review", template: "Send to legal for review" },
    { label: "Decline", template: "Decline — not a fit right now" },
  ],
  request: [
    { label: "Approve", template: "Approve the request" },
    { label: "Need info", template: "Ask for more information" },
    { label: "Delegate", template: "Forward to " },
    { label: "Decline", template: "Decline — explain why" },
    { label: "Delay", template: "Delay — follow up in 2 weeks" },
  ],
  introduction: [
    { label: "Introduce", template: "Make the introduction" },
    { label: "Decline", template: "Decline — don't feel comfortable" },
    { label: "Context", template: "Ask for more context first" },
    { label: "Loop in", template: "Loop in the right person" },
  ],
  follow_up: [
    { label: "Reply", template: "Send a quick follow-up" },
    { label: "Update", template: "Provide a status update" },
    { label: "Schedule", template: "Schedule next steps" },
    { label: "Close", template: "Close out the thread" },
  ],
  generic: [
    { label: "Reply", template: "Draft a reply" },
    { label: "Short yes", template: "Say yes briefly" },
    { label: "Short no", template: "Say no briefly" },
    { label: "Delegate", template: "Forward to " },
    { label: "Archive", template: "Archive — no response needed" },
  ],
};

// ─── Props ─────────────────────────────────────────────────────────────────

export interface QuickRepliesProps {
  category: CardCategory;
  onSelectTemplate: (template: string) => void;
  contextualValues?: Record<string, string>; // e.g. { name: "Sarah", amount: "9500" }
}

/**
 * QuickReplies — Horizontal scrollable chips for decision templates
 */
export const QuickReplies: React.FC<QuickRepliesProps> = ({
  category,
  onSelectTemplate,
  contextualValues,
}) => {
  const templates = useMemo(
    () => REPLY_TEMPLATES[category] ?? REPLY_TEMPLATES.generic,
    [category]
  );

  const handlePress = (template: QuickReplyTemplate) => {
    let filled = template.template;

    // Fill in contextual values if available
    if (contextualValues) {
      for (const [key, value] of Object.entries(contextualValues)) {
        filled = filled.replace(new RegExp(`\\{${key}\\}`, "g"), value);
      }
    }

    // If template ends with space, position cursor there (caller handles)
    onSelectTemplate(filled);
  };

  return (
    <View style={styles.container}>
      <Text style={styles.label}>Suggestions</Text>
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        contentContainerStyle={styles.scrollContent}
        keyboardShouldPersistTaps="handled"
        accessibilityRole="list"
      >
        {templates.map((template, index) => (
          <TouchableOpacity
            key={`${category}-${index}`}
            style={styles.chip}
            onPress={() => handlePress(template)}
            activeOpacity={0.7}
            accessibilityRole="button"
            accessibilityLabel={`Quick reply: ${template.label}`}
          >
            <Text style={styles.chipText}>{template.label}</Text>
          </TouchableOpacity>
        ))}
      </ScrollView>
    </View>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    width: "100%",
    marginTop: Space.md,
  },
  label: {
    ...Type.micro,
    color: Colors.textTertiary,
    textTransform: "uppercase",
    letterSpacing: 0.5,
    marginBottom: Space.sm,
    paddingHorizontal: Space.xs,
  },
  scrollContent: {
    paddingHorizontal: Space.xs,
    gap: Space.sm,
  },
  chip: {
    backgroundColor: Colors.chipBg,
    paddingHorizontal: Space.md,
    paddingVertical: Space.sm + 2,
    borderRadius: 20,
    borderWidth: 1,
    borderColor: Colors.borderLight,
    marginRight: Space.sm,
  },
  chipText: {
    ...Type.caption,
    color: Colors.chipText,
    fontWeight: "500",
  },
});

// ─── Helper: Detect card category from card data ───────────────────────────

/**
 * Detect the category of a card based on its content.
 * Used to determine which quick replies to show.
 */
export function detectCardCategory(
  theyWant: string,
  relationshipContext?: string
): CardCategory {
  const text = `${theyWant} ${relationshipContext ?? ""}`.toLowerCase();

  if (
    text.includes("price") ||
    text.includes("quote") ||
    text.includes("budget") ||
    text.includes("$") ||
    text.includes("cost") ||
    text.includes("proposal")
  ) {
    return "pricing";
  }

  if (
    text.includes("meeting") ||
    text.includes("call") ||
    text.includes("schedule") ||
    text.includes("calendar") ||
    text.includes("zoom") ||
    text.includes("availability")
  ) {
    return "meeting";
  }

  if (
    text.includes("vendor") ||
    text.includes("contract") ||
    text.includes("terms") ||
    text.includes("deliverable") ||
    text.includes("sow") ||
    text.includes("agreement")
  ) {
    return "vendor";
  }

  if (
    text.includes("intro") ||
    text.includes("connect") ||
    text.includes("introduction")
  ) {
    return "introduction";
  }

  if (
    text.includes("follow up") ||
    text.includes("following up") ||
    text.includes("checking in") ||
    text.includes("status") ||
    text.includes("update")
  ) {
    return "follow_up";
  }

  if (
    text.includes("request") ||
    text.includes("ask") ||
    text.includes("need") ||
    text.includes("can you") ||
    text.includes("would you")
  ) {
    return "request";
  }

  return "generic";
}
