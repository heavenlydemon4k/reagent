// Decision Stack — Suggestion Bar
//
// Context-aware suggestion chips that appear above the input field.
// These are extracted from card context (quoted numbers, deadlines,
// prior commitments) to help users make informed decisions quickly.

import React, { useMemo } from "react";
import {
  View,
  Text,
  TouchableOpacity,
  ScrollView,
  StyleSheet,
} from "react-native";
import { Colors, Type, Space } from "../../styles/cardStyles";
import type { CardContext } from "../../types/cards";

// ─── Props ─────────────────────────────────────────────────────────────────

export interface SuggestionBarProps {
  context: CardContext;
  onSelectSuggestion: (suggestion: string) => void;
}

interface SuggestionChip {
  type: "number" | "deadline" | "commitment" | "sentiment";
  label: string;
  value: string;
}

/**
 * SuggestionBar — Context-aware suggestions from card data
 */
export const SuggestionBar: React.FC<SuggestionBarProps> = ({
  context,
  onSelectSuggestion,
}) => {
  const suggestions = useMemo<SuggestionChip[]>(() => {
    const chips: SuggestionChip[] = [];

    // Quoted numbers → price-related suggestions
    if (context.quoted_numbers && context.quoted_numbers.length > 0) {
      for (const num of context.quoted_numbers) {
        chips.push({
          type: "number",
          label: `💰 ${num}`,
          value: `Counter at ${num}`,
        });
      }
    }

    // Deadlines → date-related suggestions
    if (context.deadlines && context.deadlines.length > 0) {
      for (const deadline of context.deadlines) {
        chips.push({
          type: "deadline",
          label: `📅 ${deadline}`,
          value: `Confirm by ${deadline}`,
        });
      }
    }

    // Prior commitments → context reminders
    if (context.prior_commitments && context.prior_commitments.length > 0) {
      for (const commitment of context.prior_commitments.slice(0, 2)) {
        // Truncate long commitments
        const truncated =
          commitment.length > 40
            ? commitment.substring(0, 37) + "..."
            : commitment;
        chips.push({
          type: "commitment",
          label: `📝 ${truncated}`,
          value: `Note: ${commitment}`,
        });
      }
    }

    return chips;
  }, [context]);

  if (suggestions.length === 0) {
    return null;
  }

  return (
    <View style={styles.container}>
      <Text style={styles.label}>From this email</Text>
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        contentContainerStyle={styles.scrollContent}
        keyboardShouldPersistTaps="handled"
      >
        {suggestions.map((suggestion, index) => (
          <TouchableOpacity
            key={`suggestion-${suggestion.type}-${index}`}
            style={[
              styles.chip,
              suggestion.type === "number" && styles.chipNumber,
              suggestion.type === "deadline" && styles.chipDeadline,
              suggestion.type === "commitment" && styles.chipCommitment,
            ]}
            onPress={() => onSelectSuggestion(suggestion.value)}
            activeOpacity={0.7}
          >
            <Text
              style={[
                styles.chipText,
                suggestion.type === "number" && styles.chipTextNumber,
              ]}
              numberOfLines={1}
            >
              {suggestion.label}
            </Text>
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
    marginBottom: Space.md,
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
    backgroundColor: Colors.bgCard,
    paddingHorizontal: Space.md,
    paddingVertical: Space.sm + 2,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: Colors.border,
    marginRight: Space.sm,
    maxWidth: 200,
  },
  chipNumber: {
    borderColor: Colors.urgentOrange,
    backgroundColor: "#FFF8F0",
  },
  chipDeadline: {
    borderColor: Colors.primary,
    backgroundColor: Colors.primaryLight,
  },
  chipCommitment: {
    borderColor: Colors.border,
    backgroundColor: Colors.chipBg,
  },
  chipText: {
    ...Type.caption,
    color: Colors.textSecondary,
  },
  chipTextNumber: {
    color: Colors.urgentOrange,
    fontWeight: "600",
  },
});
