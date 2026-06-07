// Decision Stack — Keyboard Shortcuts Help Overlay
// Modal showing all available keyboard shortcuts with descriptions.
// Triggered by pressing '?' key anywhere in the card flow.

import React from 'react';
import {
  View,
  Text,
  StyleSheet,
  Modal,
  TouchableOpacity,
  ScrollView,
  Dimensions,
} from 'react-native';
import {
  SHORTCUT_DEFINITIONS,
  type ShortcutDefinition,
} from '../../hooks/useKeyboardShortcuts';
import { useTheme } from '../../hooks/useTheme';

const { width: SCREEN_W, height: SCREEN_H } = Dimensions.get('window');

// ─── Props ─────────────────────────────────────────────────────────────────

interface ShortcutHelpOverlayProps {
  visible: boolean;
  onClose: () => void;
}

// ─── Component ─────────────────────────────────────────────────────────────

/**
 * ShortcutHelpOverlay — Modal displaying all keyboard shortcuts
 *
 * Features:
 *   - Dimmed backdrop with tap-to-dismiss
 *   - Scrollable list of all shortcuts
 *   - Clean two-column layout (key | description)
 *   - Adapts to current theme (light/dark)
 */
export const ShortcutHelpOverlay: React.FC<ShortcutHelpOverlayProps> = ({
  visible,
  onClose,
}) => {
  const { colors, isDark } = useTheme();

  return (
    <Modal
      visible={visible}
      transparent
      animationType="fade"
      onRequestClose={onClose}
      statusBarTranslucent
    >
      <TouchableOpacity
        style={[styles.backdrop, { backgroundColor: colors.scrim }]}
        onPress={onClose}
        activeOpacity={1}
        accessibilityLabel="Close shortcuts help"
        accessibilityRole="button"
      >
        <View
          style={[
            styles.container,
            {
              backgroundColor: colors.surface,
              borderColor: colors.border,
              shadowColor: colors.shadow,
            },
          ]}
          // Prevent tap-through to backdrop
          onStartShouldSetResponder={() => true}
        >
          {/* Header */}
          <View style={styles.header}>
            <Text style={[styles.title, { color: colors.textPrimary }]}
              accessibilityRole="header"
            >
              Keyboard Shortcuts
            </Text>
            <TouchableOpacity
              onPress={onClose}
              style={styles.closeButton}
              accessibilityLabel="Close"
              accessibilityRole="button"
            >
              <Text style={[styles.closeIcon, { color: colors.textSecondary }]}>
                ✕
              </Text>
            </TouchableOpacity>
          </View>

          {/* Subtitle */}
          <Text style={[styles.subtitle, { color: colors.textSecondary }]}>
            Power-user shortcuts for faster decision-making
          </Text>

          {/* Shortcuts list */}
          <ScrollView
            style={styles.scrollView}
            showsVerticalScrollIndicator={false}
            contentContainerStyle={styles.scrollContent}
          >
            {SHORTCUT_DEFINITIONS.map((shortcut) => (
              <ShortcutRow
                key={shortcut.key}
                shortcut={shortcut}
                colors={colors}
                isDark={isDark}
              />
            ))}
          </ScrollView>

          {/* Footer hint */}
          <View style={styles.footer}>
            <Text style={[styles.footerText, { color: colors.textTertiary }]}>
              Press ? anywhere to toggle this overlay
            </Text>
          </View>
        </View>
      </TouchableOpacity>
    </Modal>
  );
};

// ─── Shortcut Row ──────────────────────────────────────────────────────────

interface ShortcutRowProps {
  shortcut: ShortcutDefinition;
  colors: import('../../theme/colors').ThemeColors;
  isDark: boolean;
}

const ShortcutRow: React.FC<ShortcutRowProps> = ({
  shortcut,
  colors,
  isDark,
}) => (
  <View style={styles.row}>
    <View
      style={[
        styles.keyBadge,
        {
          backgroundColor: isDark ? colors.surfaceElevated : colors.infoMuted,
          borderColor: colors.border,
        },
      ]}
    >
      <Text
        style={[
          styles.keyText,
          { color: isDark ? colors.textPrimary : colors.info },
        ]}
      >
        {shortcut.label}
      </Text>
    </View>
    <Text style={[styles.description, { color: colors.textSecondary }]}>
      {shortcut.description}
    </Text>
  </View>
);

// ─── Styles ─────────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  backdrop: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    padding: 24,
  },
  container: {
    width: Math.min(SCREEN_W - 48, 420),
    maxHeight: SCREEN_H * 0.7,
    borderRadius: 16,
    borderWidth: 1,
    shadowOffset: { width: 0, height: 8 },
    shadowOpacity: 0.15,
    shadowRadius: 24,
    elevation: 8,
    overflow: 'hidden',
  },
  header: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 20,
    paddingTop: 16,
    paddingBottom: 8,
  },
  title: {
    fontSize: 18,
    fontWeight: '600',
    letterSpacing: -0.3,
  },
  closeButton: {
    width: 32,
    height: 32,
    justifyContent: 'center',
    alignItems: 'center',
    borderRadius: 16,
  },
  closeIcon: {
    fontSize: 16,
    fontWeight: '500',
  },
  subtitle: {
    fontSize: 13,
    paddingHorizontal: 20,
    paddingBottom: 12,
  },
  scrollView: {
    maxHeight: SCREEN_H * 0.45,
  },
  scrollContent: {
    paddingHorizontal: 20,
    paddingBottom: 8,
  },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    paddingVertical: 8,
    borderBottomWidth: 1,
    borderBottomColor: 'transparent',
  },
  keyBadge: {
    minWidth: 64,
    paddingHorizontal: 10,
    paddingVertical: 5,
    borderRadius: 8,
    alignItems: 'center',
    justifyContent: 'center',
    borderWidth: 1,
  },
  keyText: {
    fontSize: 13,
    fontWeight: '600',
    fontFamily: 'monospace',
  },
  description: {
    flex: 1,
    fontSize: 14,
    marginLeft: 14,
    fontWeight: '400',
  },
  footer: {
    paddingHorizontal: 20,
    paddingVertical: 12,
    alignItems: 'center',
  },
  footerText: {
    fontSize: 12,
    fontStyle: 'italic',
  },
});
