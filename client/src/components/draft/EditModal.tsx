// Decision Stack — Edit Modal
//
// Full-screen overlay with text editor for inline draft modification.
// Features:
// - Full-screen overlay with scrim background
// - Large text editor with the full draft body
// - Save/Cancel buttons
// - Shows original draft below for comparison
// - Preserves scroll position

import React, { useState, useEffect, useCallback } from "react";
import {
  View,
  Text,
  TextInput,
  TouchableOpacity,
  StyleSheet,
  Modal,
  ScrollView,
  KeyboardAvoidingView,
  Platform,
  Dimensions,
} from "react-native";
import { Colors, Type, Space } from "../../styles/cardStyles";

const { height: SCREEN_H } = Dimensions.get("window");

// ─── Props ─────────────────────────────────────────────────────────────────

export interface EditModalProps {
  visible: boolean;
  originalBody: string;
  currentBody: string;
  onSave: (newBody: string) => void;
  onCancel: () => void;
}

/**
 * EditModal — Full-screen inline editing overlay
 */
export const EditModal: React.FC<EditModalProps> = ({
  visible,
  originalBody,
  currentBody,
  onSave,
  onCancel,
}) => {
  const [editedText, setEditedText] = useState(currentBody);
  const [hasChanges, setHasChanges] = useState(false);

  // Sync with currentBody when modal opens
  useEffect(() => {
    if (visible) {
      setEditedText(currentBody);
      setHasChanges(false);
    }
  }, [visible, currentBody]);

  // Track changes
  useEffect(() => {
    setHasChanges(editedText !== currentBody);
  }, [editedText, currentBody]);

  const handleSave = useCallback(() => {
    if (editedText.trim().length > 0) {
      onSave(editedText.trim());
    }
  }, [editedText, onSave]);

  const handleReset = useCallback(() => {
    setEditedText(originalBody);
  }, [originalBody]);

  return (
    <Modal
      visible={visible}
      animationType="slide"
      transparent={false}
      presentationStyle="fullScreen"
      onRequestClose={onCancel}
    >
      <KeyboardAvoidingView
        style={styles.container}
        behavior={Platform.OS === "ios" ? "padding" : "height"}
      >
        {/* Header */}
        <View style={styles.header}>
          <TouchableOpacity
            style={styles.cancelButton}
            onPress={onCancel}
            activeOpacity={0.7}
            accessibilityLabel="Cancel editing"
          >
            <Text style={styles.cancelButtonText}>Cancel</Text>
          </TouchableOpacity>

          <Text style={styles.headerTitle}>Edit Draft</Text>

          <TouchableOpacity
            style={[styles.saveButton, !hasChanges && styles.saveButtonDisabled]}
            onPress={handleSave}
            activeOpacity={0.8}
            disabled={!hasChanges}
            accessibilityLabel="Save changes"
          >
            <Text
              style={[
                styles.saveButtonText,
                !hasChanges && styles.saveButtonTextDisabled,
              ]}
            >
              Save
            </Text>
          </TouchableOpacity>
        </View>

        {/* Editor */}
        <ScrollView
          style={styles.editorScroll}
          contentContainerStyle={styles.editorContent}
          keyboardShouldPersistTaps="handled"
        >
          <TextInput
            style={styles.editor}
            value={editedText}
            onChangeText={setEditedText}
            multiline
            textAlignVertical="top"
            autoFocus
            accessibilityLabel="Draft editor"
            accessibilityHint="Edit the draft text"
          />
        </ScrollView>

        {/* Comparison footer */}
        <View style={styles.footer}>
          <View style={styles.footerHeader}>
            <Text style={styles.footerLabel}>Original draft</Text>
            <TouchableOpacity
              onPress={handleReset}
              activeOpacity={0.7}
              accessibilityLabel="Reset to original"
            >
              <Text style={styles.resetText}>Reset</Text>
            </TouchableOpacity>
          </View>
          <ScrollView
            style={styles.originalScroll}
            nestedScrollEnabled
          >
            <Text style={styles.originalText}>{originalBody}</Text>
          </ScrollView>
          <Text style={styles.footerHint}>
            {hasChanges ? "Unsaved changes" : "No changes made"}
          </Text>
        </View>
      </KeyboardAvoidingView>
    </Modal>
  );
};

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: Colors.bgWarm,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: Space.md,
    paddingTop: Space.lg,
    paddingBottom: Space.md,
    backgroundColor: Colors.bgCard,
    borderBottomWidth: 1,
    borderBottomColor: Colors.border,
  },
  cancelButton: {
    paddingVertical: Space.sm,
    paddingHorizontal: Space.md,
    minWidth: 70,
  },
  cancelButtonText: {
    ...Type.body,
    color: Colors.textSecondary,
  },
  headerTitle: {
    ...Type.subtitle,
    color: Colors.textMain,
    fontWeight: "600",
  },
  saveButton: {
    backgroundColor: Colors.primary,
    paddingVertical: Space.sm,
    paddingHorizontal: Space.lg,
    borderRadius: 10,
    minWidth: 70,
    alignItems: "center",
  },
  saveButtonDisabled: {
    backgroundColor: Colors.border,
  },
  saveButtonText: {
    ...Type.captionBold,
    color: Colors.textInverse,
  },
  saveButtonTextDisabled: {
    color: Colors.textTertiary,
  },
  editorScroll: {
    flex: 1,
  },
  editorContent: {
    flexGrow: 1,
    padding: Space.lg,
  },
  editor: {
    ...Type.bodyLarge,
    color: Colors.textMain,
    lineHeight: 28,
    minHeight: SCREEN_H * 0.4,
    textAlignVertical: "top",
  },
  footer: {
    backgroundColor: Colors.bgCard,
    borderTopWidth: 1,
    borderTopColor: Colors.border,
    padding: Space.md,
    maxHeight: SCREEN_H * 0.25,
  },
  footerHeader: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: Space.sm,
  },
  footerLabel: {
    ...Type.micro,
    color: Colors.textTertiary,
    textTransform: "uppercase",
    letterSpacing: 0.5,
  },
  resetText: {
    ...Type.caption,
    color: Colors.primary,
  },
  originalScroll: {
    maxHeight: SCREEN_H * 0.15,
    backgroundColor: Colors.bgAccent,
    borderRadius: 8,
    padding: Space.sm,
  },
  originalText: {
    ...Type.caption,
    color: Colors.textSecondary,
    lineHeight: 20,
  },
  footerHint: {
    ...Type.micro,
    color: Colors.textTertiary,
    textAlign: "center",
    marginTop: Space.sm,
  },
});
