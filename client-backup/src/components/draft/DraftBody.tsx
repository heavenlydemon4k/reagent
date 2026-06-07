// Decision Stack — Draft Body Renderer
//
// Renders the AI-generated draft with:
// - Styled text in a "card" presentation
// - Highlighting of user-specific content (prices, dates) in accent color
// - Threading info: "Replying to Sarah Chen • Website Redesign"
// - Subject line display if present

import React, { useMemo } from "react";
import { View, Text, StyleSheet } from "react-native";
import { Colors, Type, Space, CardDim } from "../../styles/cardStyles";
import type { Draft } from "../../types/cards";

// ─── Props ─────────────────────────────────────────────────────────────────

export interface DraftBodyProps {
  draft: Draft;
  fromName: string;
  relationshipContext?: string;
  userDisplayName?: string;
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

/**
 * Parse draft body to highlight entities (prices, dates, emails).
 * Returns an array of segments with their highlight type.
 */
interface TextSegment {
  text: string;
  type: "normal" | "price" | "date" | "email";
}

function parseHighlightedText(text: string): TextSegment[] {
  const segments: TextSegment[] = [];

  // Patterns for highlighting
  const patterns: { regex: RegExp; type: TextSegment["type"] }[] = [
    {
      // Dollar amounts: $9,500 / $9500 / $ 9,500
      regex: /\$\s*[\d,]+(?:\.\d{2})?/g,
      type: "price",
    },
    {
      // Date references: January 15, Jan 15, 01/15/2024, next Tuesday
      regex: /\b(?:January|February|March|April|May|June|July|August|September|October|November|December|Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}(?:,\s+\d{4})?\b|\b\d{1,2}\/\d{1,2}(?:\/\d{2,4})?\b|\b(?:next|this)\s+(?:Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday|week|month)\b/gi,
      type: "date",
    },
    {
      // Email addresses
      regex: /[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}/g,
      type: "email",
    },
  ];

  // Find all matches across all patterns
  interface Match {
    start: number;
    end: number;
    type: TextSegment["type"];
    text: string;
  }

  const allMatches: Match[] = [];
  for (const { regex, type } of patterns) {
    let match;
    while ((match = regex.exec(text)) !== null) {
      allMatches.push({
        start: match.index,
        end: match.index + match[0].length,
        type,
        text: match[0],
      });
    }
  }

  // Sort by position
  allMatches.sort((a, b) => a.start - b.start);

  // Remove overlapping matches (keep first)
  const filteredMatches: Match[] = [];
  let lastEnd = -1;
  for (const match of allMatches) {
    if (match.start >= lastEnd) {
      filteredMatches.push(match);
      lastEnd = match.end;
    }
  }

  // Build segments
  let currentPos = 0;
  for (const match of filteredMatches) {
    if (match.start > currentPos) {
      segments.push({
        text: text.slice(currentPos, match.start),
        type: "normal",
      });
    }
    segments.push({
      text: match.text,
      type: match.type,
    });
    currentPos = match.end;
  }

  // Remaining text
  if (currentPos < text.length) {
    segments.push({
      text: text.slice(currentPos),
      type: "normal",
    });
  }

  // If no matches, return the whole text as normal
  if (segments.length === 0) {
    segments.push({ text, type: "normal" });
  }

  return segments;
}

/**
 * DraftBody — Rendered draft with entity highlighting and threading info
 */
export const DraftBody: React.FC<DraftBodyProps> = ({
  draft,
  fromName,
  relationshipContext,
  userDisplayName = "[You]",
}) => {
  const segments = useMemo(
    () => parseHighlightedText(draft.draft_body),
    [draft.draft_body]
  );

  return (
    <View style={styles.container}>
      {/* Threading info header */}
      <View style={styles.threadHeader}>
        <Text style={styles.threadLabel}>Replying to</Text>
        <Text style={styles.threadName}>{fromName}</Text>
        {relationshipContext && (
          <Text style={styles.threadContext}>{relationshipContext}</Text>
        )}
      </View>

      {/* Subject line if present */}
      {draft.subject_line && (
        <View style={styles.subjectContainer}>
          <Text style={styles.subjectLabel}>Subject:</Text>
          <Text style={styles.subjectText}>{draft.subject_line}</Text>
        </View>
      )}

      {/* Draft body with highlighted entities */}
      <View style={styles.bodyContainer}>
        {segments.map((segment, index) => (
          <Text
            key={`seg-${index}`}
            style={[
              styles.bodyText,
              segment.type === "price" && styles.highlightPrice,
              segment.type === "date" && styles.highlightDate,
              segment.type === "email" && styles.highlightEmail,
            ]}
          >
            {segment.text}
          </Text>
        ))}
      </View>

      {/* Signature placeholder */}
      <View style={styles.signatureContainer}>
        <Text style={styles.signatureText}>{userDisplayName}</Text>
      </View>

      {/* Model info (subtle, for transparency) */}
      {draft.model_used && (
        <Text style={styles.modelInfo}>
          Drafted {draft.tokens_used ? `· ${draft.tokens_used} tokens` : ""}
        </Text>
      )}
    </View>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    width: "100%",
    backgroundColor: Colors.bgCard,
    borderRadius: CardDim.borderRadius,
    padding: CardDim.innerPad,
    borderWidth: 1,
    borderColor: Colors.borderLight,
  },
  threadHeader: {
    paddingBottom: Space.md,
    borderBottomWidth: 1,
    borderBottomColor: Colors.borderLight,
    marginBottom: Space.md,
  },
  threadLabel: {
    ...Type.micro,
    color: Colors.textTertiary,
    textTransform: "uppercase",
    letterSpacing: 0.5,
  },
  threadName: {
    ...Type.subtitle,
    color: Colors.textMain,
    marginTop: Space.xs,
  },
  threadContext: {
    ...Type.caption,
    color: Colors.textSecondary,
    marginTop: 2,
  },
  subjectContainer: {
    flexDirection: "row",
    alignItems: "baseline",
    marginBottom: Space.md,
    paddingBottom: Space.sm,
    borderBottomWidth: 1,
    borderBottomColor: Colors.borderLight,
  },
  subjectLabel: {
    ...Type.caption,
    color: Colors.textTertiary,
    marginRight: Space.sm,
  },
  subjectText: {
    ...Type.captionBold,
    color: Colors.textMain,
    flex: 1,
  },
  bodyContainer: {
    flexDirection: "row",
    flexWrap: "wrap",
  },
  bodyText: {
    ...Type.bodyLarge,
    color: Colors.textMain,
    lineHeight: 26,
  },
  highlightPrice: {
    color: Colors.urgentOrange,
    fontWeight: "600",
    backgroundColor: "#FFF8F0",
    borderRadius: 4,
    overflow: "hidden",
    paddingHorizontal: 2,
  },
  highlightDate: {
    color: Colors.primary,
    fontWeight: "500",
    backgroundColor: Colors.primaryLight,
    borderRadius: 4,
    overflow: "hidden",
    paddingHorizontal: 2,
  },
  highlightEmail: {
    color: Colors.secondary,
    fontWeight: "500",
    textDecorationLine: "underline",
    textDecorationColor: Colors.border,
  },
  signatureContainer: {
    marginTop: Space.lg,
    paddingTop: Space.md,
    borderTopWidth: 1,
    borderTopColor: Colors.borderLight,
  },
  signatureText: {
    ...Type.body,
    color: Colors.textSecondary,
    fontStyle: "italic",
  },
  modelInfo: {
    ...Type.micro,
    color: Colors.textTertiary,
    textAlign: "right",
    marginTop: Space.sm,
  },
});
