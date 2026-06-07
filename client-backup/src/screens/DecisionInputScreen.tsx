// Decision Stack — Decision Input Screen
//
// Triggered by [Decide] button on a card. Shows card summary at top
// for context, then a large input field for the user's one-line decision.
// Quick replies and context-aware suggestions help speed up input.
// Submit → POST /cards/:id/decide → navigate to DraftReviewScreen.

import React, { useState, useCallback } from "react";
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  ScrollView,
  KeyboardAvoidingView,
  Platform,
  Alert,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Colors, Type, Space, CardDim } from "../styles/cardStyles";
import type { DecisionCard } from "../types/cards";
import { useDrafting } from "../hooks/useDrafting";
import { TextInputField } from "../components/decision/TextInputField";
import {
  QuickReplies,
  detectCardCategory,
} from "../components/decision/QuickReplies";
import { SuggestionBar } from "../components/decision/SuggestionBar";
import { LoadingSpinner } from "../components/common/LoadingSpinner";

// ─── Props ─────────────────────────────────────────────────────────────────

export interface DecisionInputScreenProps {
  card: DecisionCard;
  onDraftReady: (draft: ReturnType<typeof useDrafting>["draft"]) => void;
  onCancel: () => void;
}

/**
 * DecisionInputScreen — Text input for one-line decision instructions
 *
 * Layout:
 *   Card summary (from, they_want)
 *   Suggestion bar (context-aware)
 *   Input field (large, centered)
 *   Quick reply chips
 *   [Submit] button
 */
export const DecisionInputScreen: React.FC<DecisionInputScreenProps> = ({
  card,
  onDraftReady,
  onCancel,
}) => {
  const [input, setInput] = useState("");
  const { submitDecision, isLoading, error } = useDrafting(card.id);

  // Detect card category for quick replies
  const category = detectCardCategory(
    card.they_want,
    card.from.relationship_context
  );

  // Handle template selection from quick replies
  const handleTemplateSelect = useCallback((template: string) => {
    setInput(template);
    // If template ends with space, user probably wants to add more
  }, []);

  // Handle suggestion selection from context bar
  const handleSuggestionSelect = useCallback((suggestion: string) => {
    setInput((prev) => {
      if (prev.length > 0) {
        return `${prev} — ${suggestion}`;
      }
      return suggestion;
    });
  }, []);

  // Handle submit
  const handleSubmit = useCallback(async () => {
    const trimmed = input.trim();
    if (trimmed.length === 0) {
      return;
    }

    if (trimmed.length > 200) {
      Alert.alert(
        "Input too long",
        "Please keep your instruction under 200 characters for best results."
      );
      return;
    }

    try {
      const draft = await submitDecision(trimmed);
      if (draft) {
        onDraftReady(draft);
      }
    } catch {
      // Error is handled in the hook — error state will be set
    }
  }, [input, submitDecision, onDraftReady]);

  // Show loading state while draft generates
  if (isLoading) {
    return (
      <SafeAreaView style={styles.container}>
        <View style={styles.loadingOverlay}>
          <LoadingSpinner message="Drafting..." />
          <Text style={styles.loadingSubtext}>
            AI is writing your response
          </Text>
        </View>
      </SafeAreaView>
    );
  }

  return (
    <SafeAreaView style={styles.container}>
      <KeyboardAvoidingView
        style={styles.keyboardView}
        behavior={Platform.OS === "ios" ? "padding" : "height"}
        keyboardVerticalOffset={Platform.OS === "ios" ? 64 : 0}
      >
        <ScrollView
          style={styles.scrollView}
          contentContainerStyle={styles.scrollContent}
          keyboardShouldPersistTaps="handled"
        >
          {/* Header with card summary */}
          <View style={styles.header}>
            <TouchableOpacity
              style={styles.backButton}
              onPress={onCancel}
              activeOpacity={0.7}
            >
              <Text style={styles.backButtonText}>← Back</Text>
            </TouchableOpacity>
            <Text style={styles.headerTitle}>Your Decision</Text>
            <View style={styles.backButtonPlaceholder} />
          </View>

          {/* Card summary for context */}
          <View style={styles.cardSummary}>
            <Text style={styles.cardFrom}>{card.from.name}</Text>
            {card.from.relationship_context && (
              <Text style={styles.cardContext}>
                {card.from.relationship_context}
              </Text>
            )}
            <View style={styles.theyWantContainer}>
              <Text style={styles.theyWantLabel}>They want</Text>
              <Text style={styles.theyWantText}>{card.they_want}</Text>
            </View>
          </View>

          {/* Context-aware suggestions */}
          <SuggestionBar
            context={card.context}
            onSelectSuggestion={handleSuggestionSelect}
          />

          {/* Main input field */}
          <View style={styles.inputSection}>
            <Text style={styles.inputLabel}>What should we do?</Text>
            <TextInputField
              value={input}
              onChangeText={setInput}
              onSubmit={handleSubmit}
              autoFocus
            />
          </View>

          {/* Quick reply chips */}
          <QuickReplies
            category={category}
            onSelectTemplate={handleTemplateSelect}
          />

          {/* Error message */}
          {error && (
            <View style={styles.errorContainer}>
              <Text style={styles.errorText}>{error}</Text>
            </View>
          )}
        </ScrollView>

        {/* Submit button — fixed at bottom */}
        <View style={styles.footer}>
          <TouchableOpacity
            style={[
              styles.submitButton,
              (input.trim().length === 0 || isLoading) &&
                styles.submitButtonDisabled,
            ]}
            onPress={handleSubmit}
            activeOpacity={0.85}
            disabled={input.trim().length === 0 || isLoading}
            accessibilityLabel="Submit decision"
            accessibilityHint="Send your instruction to the AI for drafting"
          >
            <Text style={styles.submitButtonText}>
              {isLoading ? "Drafting..." : "Submit"}
            </Text>
          </TouchableOpacity>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: Colors.bgWarm,
  },
  keyboardView: {
    flex: 1,
  },
  scrollView: {
    flex: 1,
  },
  scrollContent: {
    padding: Space.lg,
    paddingBottom: Space.xxl,
  },
  loadingOverlay: {
    flex: 1,
    justifyContent: "center",
    alignItems: "center",
    padding: Space.lg,
  },
  loadingSubtext: {
    ...Type.caption,
    color: Colors.textTertiary,
    marginTop: Space.md,
    textAlign: "center",
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: Space.lg,
  },
  backButton: {
    paddingVertical: Space.sm,
    minWidth: 60,
  },
  backButtonText: {
    ...Type.body,
    color: Colors.textSecondary,
  },
  backButtonPlaceholder: {
    minWidth: 60,
  },
  headerTitle: {
    ...Type.title,
    color: Colors.textMain,
    fontWeight: "600",
  },
  cardSummary: {
    backgroundColor: Colors.bgCard,
    borderRadius: CardDim.borderRadius,
    padding: CardDim.innerPad,
    marginBottom: Space.lg,
    borderWidth: 1,
    borderColor: Colors.borderLight,
    shadowColor: "#000",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.04,
    shadowRadius: 6,
    elevation: 2,
  },
  cardFrom: {
    ...Type.subtitle,
    color: Colors.textMain,
  },
  cardContext: {
    ...Type.caption,
    color: Colors.textSecondary,
    marginTop: 2,
  },
  theyWantContainer: {
    marginTop: Space.md,
    paddingTop: Space.md,
    borderTopWidth: 1,
    borderTopColor: Colors.borderLight,
  },
  theyWantLabel: {
    ...Type.micro,
    color: Colors.textTertiary,
    textTransform: "uppercase",
    letterSpacing: 0.5,
    marginBottom: Space.xs,
  },
  theyWantText: {
    ...Type.body,
    color: Colors.textMain,
    lineHeight: 23,
  },
  inputSection: {
    marginTop: Space.sm,
    marginBottom: Space.md,
    width: "100%",
  },
  inputLabel: {
    ...Type.captionBold,
    color: Colors.textSecondary,
    marginBottom: Space.md,
    textAlign: "center",
  },
  errorContainer: {
    backgroundColor: Colors.urgentBg,
    borderRadius: 10,
    padding: Space.md,
    marginTop: Space.md,
    borderLeftWidth: 3,
    borderLeftColor: Colors.urgentRed,
  },
  errorText: {
    ...Type.caption,
    color: Colors.urgentRed,
    lineHeight: 20,
  },
  footer: {
    padding: Space.lg,
    backgroundColor: Colors.bgWarm,
    borderTopWidth: 1,
    borderTopColor: Colors.borderLight,
  },
  submitButton: {
    width: "100%",
    backgroundColor: Colors.primary,
    paddingVertical: Space.md,
    borderRadius: 14,
    alignItems: "center",
    justifyContent: "center",
    minHeight: 52,
  },
  submitButtonDisabled: {
    backgroundColor: Colors.border,
  },
  submitButtonText: {
    ...Type.subtitle,
    color: Colors.textInverse,
    fontWeight: "600",
  },
});
