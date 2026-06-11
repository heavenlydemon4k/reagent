// Decision Stack — Suggested Action Chips
// Tappable chips shown above the input when the assistant suggests an action

import React from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  ScrollView,
} from 'react-native';
import { palette } from '@theme/colors';
import { fontSize, fontWeight } from '@theme/typography';
import { spacing } from '@theme/spacing';
import type { ChatResponse } from '../../types/cards';

export type SuggestedActionType = NonNullable<ChatResponse['suggested_action']>;

interface SuggestedActionProps {
  action: SuggestedActionType;
  onPress: (action: SuggestedActionType, targetId?: string) => void;
  targetId?: string | null;
}

/**
 * Map action types to human-readable labels and icons.
 */
function getActionMeta(action: SuggestedActionType): {
  label: string;
  emoji: string;
} {
  switch (action) {
    case 'clear_batch':
      return { label: 'Clear my batch', emoji: '✓' };
    case 'view_card':
      return { label: 'View card', emoji: '◈' };
    case 'schedule':
      return { label: 'Check my calendar', emoji: '◷' };
    case 'none':
    default:
      return { label: 'Continue', emoji: '→' };
  }
}

/**
 * SuggestedAction — a scrollable row of action chips above the chat input.
 * Appears when the assistant response includes a suggested_action.
 * Examples: "Clear my batch", "What about Sarah?", "Check my calendar"
 */
export const SuggestedAction: React.FC<SuggestedActionProps> = ({
  action,
  onPress,
  targetId,
}) => {
  if (action === 'none') return null;

  const meta = getActionMeta(action);

  return (
    <View style={styles.container}>
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        contentContainerStyle={styles.scrollContent}
        keyboardShouldPersistTaps="handled"
      >
        <TouchableOpacity
          onPress={() => onPress(action, targetId ?? undefined)}
          style={styles.chip}
          activeOpacity={0.7}
          accessibilityLabel={`Suggested action: ${meta.label}`}
          accessibilityRole="button"
        >
          <Text style={styles.emoji}>{meta.emoji}</Text>
          <Text style={styles.label}>{meta.label}</Text>
        </TouchableOpacity>

        {/* Additional contextual chips can be added here */}
        {action === 'clear_batch' && (
          <TouchableOpacity
            onPress={() => onPress('view_card', targetId ?? undefined)}
            style={styles.chipSecondary}
            activeOpacity={0.7}
            accessibilityLabel="Review first"
            accessibilityRole="button"
          >
            <Text style={styles.labelSecondary}>Review first</Text>
          </TouchableOpacity>
        )}
      </ScrollView>
    </View>
  );
};

const styles = StyleSheet.create({
  container: {
    backgroundColor: palette.ink[50],
    paddingHorizontal: spacing[4],
    paddingTop: spacing[2],
    paddingBottom: spacing[1],
  },
  scrollContent: {
    flexDirection: 'row',
    gap: spacing[2],
  },
  chip: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: palette.sand[400],
    borderRadius: spacing[5],
    paddingHorizontal: spacing[4],
    paddingVertical: spacing[2.5],
    gap: spacing[1.5],
  },
  chipSecondary: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: '#ffffff',
    borderRadius: spacing[5],
    paddingHorizontal: spacing[4],
    paddingVertical: spacing[2.5],
    borderWidth: 1,
    borderColor: palette.ink[200],
  },
  emoji: {
    fontSize: fontSize.sm,
  },
  label: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.semibold,
    color: '#ffffff',
  },
  labelSecondary: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.medium,
    color: palette.ink[700],
  },
});
