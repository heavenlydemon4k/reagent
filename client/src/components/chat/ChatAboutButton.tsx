// Decision Stack — "Chat about this" Button
// Integration point from any card to open a conversation linked to that card

import React, { useCallback } from 'react';
import {
  TouchableOpacity,
  Text,
  StyleSheet,
} from 'react-native';
import { useNavigation } from '@react-navigation/native';
import { palette } from '@theme/colors';
import { fontSize, fontWeight } from '@theme/typography';
import { spacing } from '@theme/spacing';
import { useConversations } from '@hooks/useConversations';
import type { NativeStackNavigationProp } from '@react-navigation/native-stack';
import type { RootStackParamList } from '@navigation/AppNavigator';

type NavigationProp = NativeStackNavigationProp<RootStackParamList>;

interface ChatAboutButtonProps {
  cardId: string;
  threadId?: string;
  label?: string;
  variant?: 'inline' | 'standalone';
}

/**
 * ChatAboutButton — opens a chat conversation linked to a specific card.
 * Used from card detail views to ask questions about that decision.
 *
 * Flow:
 * 1. User taps "Chat about this" on a card
 * 2. Creates (or reuses) a conversation linked to cardId
 * 3. Navigates to ChatScreen with the linked card context
 */
export const ChatAboutButton: React.FC<ChatAboutButtonProps> = ({
  cardId,
  threadId,
  label = 'Chat about this',
  variant = 'standalone',
}) => {
  const navigation = useNavigation<NavigationProp>();
  const { createConversation } = useConversations();

  const handlePress = useCallback(async () => {
    // Create a new conversation linked to this card
    const conv = await createConversation(cardId);
    if (conv) {
      navigation.navigate('Chat', {
        conversationId: conv.id,
        linkedCardId: cardId,
      });
    }
  }, [cardId, createConversation, navigation]);

  if (variant === 'inline') {
    return (
      <TouchableOpacity
        onPress={handlePress}
        style={styles.inlineContainer}
        activeOpacity={0.7}
        accessibilityLabel={label}
        accessibilityRole="button"
      >
        <Text style={styles.inlineText}>💬 {label}</Text>
      </TouchableOpacity>
    );
  }

  return (
    <TouchableOpacity
      onPress={handlePress}
      style={styles.container}
      activeOpacity={0.7}
      accessibilityLabel={label}
      accessibilityRole="button"
    >
      <Text style={styles.icon}>💬</Text>
      <Text style={styles.text}>{label}</Text>
    </TouchableOpacity>
  );
};

const styles = StyleSheet.create({
  container: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: palette.sand[50],
    borderRadius: spacing[2.5],
    paddingVertical: spacing[3],
    paddingHorizontal: spacing[5],
    borderWidth: 1,
    borderColor: palette.sand[200],
    gap: spacing[2],
  },
  icon: {
    fontSize: fontSize.sm,
  },
  text: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.semibold,
    color: palette.sand[700],
  },
  inlineContainer: {
    paddingVertical: spacing[1],
  },
  inlineText: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.medium,
    color: palette.steel[600],
  },
});
