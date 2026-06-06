// Decision Stack — Conversation List Item
// iMessage/WhatsApp-style conversation card for ChatListScreen

import React from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  Animated,
} from 'react-native';
import { Swipeable } from 'react-native-gesture-handler';
import { palette } from '@theme/colors';
import { fontSize, fontWeight, lineHeight } from '@theme/typography';
import { spacing } from '@theme/spacing';
import type { ConversationListItem } from '@types/cards';

interface ConversationCardProps {
  conversation: ConversationListItem;
  onPress: (id: string) => void;
  onDelete: (id: string) => void;
}

/**
 * Format a timestamp into a human-readable relative time.
 */
function formatRelativeTime(isoTimestamp: string): string {
  const date = new Date(isoTimestamp);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMins / 60);
  const diffDays = Math.floor(diffHours / 24);

  if (diffMins < 1) return 'now';
  if (diffMins < 60) return `${diffMins}m`;
  if (diffHours < 24) return `${diffHours}h`;
  if (diffDays < 7) return `${diffDays}d`;
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

/**
 * ConversationCard — a swipeable row showing conversation preview.
 * Layout: avatar placeholder | title + last message | timestamp + count
 */
export const ConversationCard: React.FC<ConversationCardProps> = ({
  conversation,
  onPress,
  onDelete,
}) => {
  const renderRightActions = (
    _progress: Animated.AnimatedInterpolation<number>,
    dragX: Animated.AnimatedInterpolation<number>
  ) => {
    const translateX = dragX.interpolate({
      inputRange: [-80, 0],
      outputRange: [0, 80],
      extrapolate: 'clamp',
    });

    return (
      <Animated.View
        style={[
          styles.deleteAction,
          { transform: [{ translateX }] },
        ]}
      >
        <TouchableOpacity
          onPress={() => onDelete(conversation.id)}
          style={styles.deleteButton}
          accessibilityLabel="Delete conversation"
          accessibilityRole="button"
        >
          <Text style={styles.deleteText}>Delete</Text>
        </TouchableOpacity>
      </Animated.View>
    );
  };

  const hasUnread = conversation.message_count > 0;

  return (
    <Swipeable
      renderRightActions={renderRightActions}
      friction={2}
      rightThreshold={40}
    >
      <TouchableOpacity
        onPress={() => onPress(conversation.id)}
        style={styles.container}
        activeOpacity={0.7}
        accessibilityLabel={`Conversation: ${conversation.title}`}
        accessibilityRole="button"
      >
        {/* Avatar Placeholder */}
        <View style={styles.avatar}>
          <Text style={styles.avatarText}>
            {conversation.title.charAt(0).toUpperCase()}
          </Text>
        </View>

        {/* Content */}
        <View style={styles.content}>
          <View style={styles.topRow}>
            <Text style={styles.title} numberOfLines={1}>
              {conversation.title}
            </Text>
            <Text style={styles.timestamp}>
              {formatRelativeTime(conversation.updated_at)}
            </Text>
          </View>

          <View style={styles.bottomRow}>
            <Text style={styles.preview} numberOfLines={2}>
              {conversation.last_message_preview}
            </Text>
            {hasUnread && (
              <View style={styles.countBadge}>
                <Text style={styles.countText}>
                  {conversation.message_count}
                </Text>
              </View>
            )}
          </View>
        </View>
      </TouchableOpacity>
    </Swipeable>
  );
};

const styles = StyleSheet.create({
  container: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    paddingHorizontal: spacing[4],
    paddingVertical: spacing[3],
    backgroundColor: '#ffffff',
    borderBottomWidth: 1,
    borderBottomColor: palette.ink[50],
  },
  avatar: {
    width: spacing[12],
    height: spacing[12],
    borderRadius: spacing[6],
    backgroundColor: palette.sand[100],
    justifyContent: 'center',
    alignItems: 'center',
    marginRight: spacing[3],
  },
  avatarText: {
    fontSize: fontSize.lg,
    fontWeight: fontWeight.semibold,
    color: palette.sand[600],
  },
  content: {
    flex: 1,
    justifyContent: 'center',
  },
  topRow: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    marginBottom: spacing[1],
  },
  title: {
    fontSize: fontSize.base,
    fontWeight: fontWeight.semibold,
    color: palette.ink[900],
    flex: 1,
    marginRight: spacing[2],
  },
  timestamp: {
    fontSize: fontSize.xs,
    color: palette.ink[400],
    fontWeight: fontWeight.normal,
  },
  bottomRow: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'flex-start',
  },
  preview: {
    flex: 1,
    fontSize: fontSize.sm,
    color: palette.ink[500],
    lineHeight: lineHeight.normal * fontSize.sm,
    marginRight: spacing[2],
  },
  countBadge: {
    minWidth: spacing[5],
    height: spacing[5],
    borderRadius: spacing[2.5],
    backgroundColor: palette.sand[400],
    justifyContent: 'center',
    alignItems: 'center',
    paddingHorizontal: spacing[1.5],
    marginTop: spacing[0.5],
  },
  countText: {
    fontSize: fontSize.xs,
    fontWeight: fontWeight.semibold,
    color: '#ffffff',
  },
  deleteAction: {
    backgroundColor: palette.rose[500],
    justifyContent: 'center',
    alignItems: 'center',
    width: 80,
  },
  deleteButton: {
    flex: 1,
    justifyContent: 'center',
    alignItems: 'center',
    width: '100%',
  },
  deleteText: {
    color: '#ffffff',
    fontSize: fontSize.sm,
    fontWeight: fontWeight.semibold,
  },
});
