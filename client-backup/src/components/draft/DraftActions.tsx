// Decision Stack — Draft Actions
//
// Action buttons for the draft review screen:
// - [Edit] → secondary style, opens inline editing modal
// - [Shorten] → tertiary text button, re-submits with "make it shorter"
// - [Formalize] → tertiary text button, re-submits with "make it more formal"
// - [Approve and Send] → primary full-width button (green accent)
// - [Back] → minimal text link, returns to decision input

import React from "react";
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  ActivityIndicator,
} from "react-native";
import { Colors, Type, Space, CardDim } from "../../styles/cardStyles";

// ─── Props ─────────────────────────────────────────────────────────────────

export interface DraftActionsProps {
  onEdit: () => void;
  onShorten: () => void;
  onFormalize: () => void;
  onApprove: () => void;
  onBack: () => void;
  isLoading?: boolean;
  isApproved?: boolean;
}

/**
 * DraftActions — Button group for draft review
 */
export const DraftActions: React.FC<DraftActionsProps> = ({
  onEdit,
  onShorten,
  onFormalize,
  onApprove,
  onBack,
  isLoading = false,
  isApproved = false,
}) => {
  return (
    <View style={styles.container}>
      {/* Secondary actions row: [Edit] [Shorten] [Formalize] */}
      <View style={styles.secondaryRow}>
        {/* Edit button — secondary style */}
        <TouchableOpacity
          style={styles.editButton}
          onPress={onEdit}
          activeOpacity={0.8}
          disabled={isLoading || isApproved}
          accessibilityLabel="Edit draft"
          accessibilityHint="Open full editor to modify the draft"
        >
          <Text style={styles.editButtonText}>✎ Edit</Text>
        </TouchableOpacity>

        <View style={styles.tertiaryGroup}>
          {/* Shorten — tertiary text button */}
          <TouchableOpacity
            style={styles.tertiaryButton}
            onPress={onShorten}
            activeOpacity={0.7}
            disabled={isLoading || isApproved}
            accessibilityLabel="Make shorter"
            accessibilityHint="Re-draft to make the response shorter"
          >
            <Text style={styles.tertiaryButtonText}>Shorten</Text>
          </TouchableOpacity>

          {/* Formalize — tertiary text button */}
          <TouchableOpacity
            style={styles.tertiaryButton}
            onPress={onFormalize}
            activeOpacity={0.7}
            disabled={isLoading || isApproved}
            accessibilityLabel="Make more formal"
            accessibilityHint="Re-draft to make the response more formal"
          >
            <Text style={styles.tertiaryButtonText}>Formalize</Text>
          </TouchableOpacity>
        </View>
      </View>

      {/* Loading indicator */}
      {isLoading && (
        <View style={styles.loadingContainer}>
          <ActivityIndicator size="small" color={Colors.primary} />
          <Text style={styles.loadingText}>Re-drafting...</Text>
        </View>
      )}

      {/* Approve button — primary full-width */}
      <TouchableOpacity
        style={[
          styles.approveButton,
          (isLoading || isApproved) && styles.approveButtonDisabled,
        ]}
        onPress={onApprove}
        activeOpacity={0.85}
        disabled={isLoading || isApproved}
        accessibilityLabel="Approve and send"
        accessibilityHint="Confirm approval and queue draft for sending"
      >
        <Text style={styles.approveButtonText}>
          {isApproved ? "✓ Approved" : "Approve and Send"}
        </Text>
      </TouchableOpacity>

      {/* Back link — minimal text */}
      <TouchableOpacity
        style={styles.backLink}
        onPress={onBack}
        activeOpacity={0.7}
        disabled={isLoading}
        accessibilityLabel="Go back"
        accessibilityHint="Return to decision input to re-enter your instruction"
      >
        <Text style={styles.backLinkText}>← Back to input</Text>
      </TouchableOpacity>
    </View>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    width: "100%",
    paddingHorizontal: Space.xs,
  },
  secondaryRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: Space.md,
  },
  editButton: {
    backgroundColor: Colors.bgAccent,
    paddingVertical: Space.sm + 2,
    paddingHorizontal: Space.lg,
    borderRadius: 12,
    borderWidth: 1,
    borderColor: Colors.border,
  },
  editButtonText: {
    ...Type.captionBold,
    color: Colors.textSecondary,
  },
  tertiaryGroup: {
    flexDirection: "row",
    alignItems: "center",
  },
  tertiaryButton: {
    paddingVertical: Space.sm,
    paddingHorizontal: Space.md,
    marginLeft: Space.sm,
  },
  tertiaryButtonText: {
    ...Type.caption,
    color: Colors.primary,
    textDecorationLine: "underline",
    textDecorationColor: Colors.primaryLight,
  },
  loadingContainer: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    marginBottom: Space.md,
    gap: Space.sm,
  },
  loadingText: {
    ...Type.caption,
    color: Colors.textTertiary,
  },
  approveButton: {
    width: "100%",
    backgroundColor: "#5B8C5A", // Sage green — calm approval accent
    paddingVertical: Space.md,
    borderRadius: 14,
    alignItems: "center",
    justifyContent: "center",
    minHeight: 52,
    shadowColor: "#5B8C5A",
    shadowOffset: { width: 0, height: 2 },
    shadowOpacity: 0.15,
    shadowRadius: 6,
    elevation: 3,
  },
  approveButtonDisabled: {
    backgroundColor: "#a8bf9d",
    shadowOpacity: 0,
    elevation: 0,
  },
  approveButtonText: {
    ...Type.subtitle,
    color: Colors.textInverse,
    fontWeight: "600",
  },
  backLink: {
    alignSelf: "center",
    marginTop: Space.md,
    paddingVertical: Space.sm,
    paddingHorizontal: Space.lg,
  },
  backLinkText: {
    ...Type.caption,
    color: Colors.textTertiary,
  },
});
