// Decision Stack — Scrollable Message List
// Auto-scrolls to bottom when new messages arrive, supports pull-to-refresh

import React, { useRef, useEffect, useCallback } from 'react';
import {
  View,
  FlatList,
  StyleSheet,
  KeyboardAvoidingView,
  Platform,
} from 'react-native';
import { palette } from '@theme/colors';
import { spacing } from '@theme/spacing';
import type { ChatMessage, ChunkCitation } from '@types/cards';
import { MessageBubble } from './MessageBubble';

interface MessageListProps {
  messages: ChatMessage[];
  isLoading: boolean;
  onPressCitation: (citation: ChunkCitation) => void;
  onPlayAudio?: (audioUrl: string) => void;
  currentlyPlayingAudioUrl?: string | null;
}

/**
 * Typing indicator bubble shown while assistant is generating a response.
 */
const TypingIndicator: React.FC = () => (
  <View style={styles.typingContainer}>
    <View style={styles.typingBubble}>
      <View style={styles.dotRow}>
        <View style={[styles.dot, styles.dot1]} />
        <View style={[styles.dot, styles.dot2]} />
        <View style={[styles.dot, styles.dot3]} />
      </View>
    </View>
  </View>
);

/**
 * MessageList — scrollable, auto-scrolling list of chat messages.
 * Uses FlatList for performance with long conversation histories.
 */
export const MessageList: React.FC<MessageListProps> = ({
  messages,
  isLoading,
  onPressCitation,
  onPlayAudio,
  currentlyPlayingAudioUrl,
}) => {
  const flatListRef = useRef<FlatList<ChatMessage>>(null);

  // Auto-scroll to bottom when messages change
  useEffect(() => {
    if (messages.length > 0 && flatListRef.current) {
      // Small delay to ensure layout is complete
      const timer = setTimeout(() => {
        flatListRef.current?.scrollToEnd({ animated: true });
      }, 100);
      return () => clearTimeout(timer);
    }
  }, [messages.length, messages[messages.length - 1]?.id]);

  const renderItem = useCallback(
    ({ item }: { item: ChatMessage }) => (
      <MessageBubble
        message={item}
        onPressCitation={onPressCitation}
        onPlayAudio={onPlayAudio}
        isPlayingAudio={
          item.audio_url ? item.audio_url === currentlyPlayingAudioUrl : false
        }
      />
    ),
    [onPressCitation, onPlayAudio, currentlyPlayingAudioUrl]
  );

  const keyExtractor = useCallback((item: ChatMessage) => item.id, []);

  return (
    <KeyboardAvoidingView
      style={styles.container}
      behavior={Platform.OS === 'ios' ? 'padding' : undefined}
      keyboardVerticalOffset={Platform.OS === 'ios' ? 90 : 0}
    >
      <FlatList
        ref={flatListRef}
        data={messages}
        renderItem={renderItem}
        keyExtractor={keyExtractor}
        contentContainerStyle={styles.contentContainer}
        showsVerticalScrollIndicator={false}
        keyboardShouldPersistTaps="handled"
        keyboardDismissMode="on-drag"
        initialNumToRender={15}
        maxToRenderPerBatch={10}
        windowSize={10}
        maintainVisibleContentPosition={{
          minIndexForVisible: 0,
        }}
        ListEmptyComponent={
          <View style={styles.emptyContainer}>
            {/* Empty state — could show welcome illustration */}
          </View>
        }
        ListFooterComponent={isLoading ? <TypingIndicator /> : null}
      />
    </KeyboardAvoidingView>
  );
};

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: palette.ink[50],
  },
  contentContainer: {
    paddingTop: spacing[4],
    paddingBottom: spacing[6],
    flexGrow: 1,
  },
  emptyContainer: {
    flex: 1,
  },
  typingContainer: {
    flexDirection: 'row',
    alignSelf: 'flex-start',
    marginVertical: spacing[1.5],
    marginLeft: spacing[13], // aligns with assistant bubbles
  },
  typingBubble: {
    backgroundColor: '#ffffff',
    borderRadius: spacing[3],
    borderBottomLeftRadius: spacing[1],
    paddingHorizontal: spacing[4],
    paddingVertical: spacing[3],
    borderWidth: 1,
    borderColor: palette.ink[100],
    minWidth: spacing[16],
  },
  dotRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: spacing[1.5],
  },
  dot: {
    width: spacing[1.5],
    height: spacing[1.5],
    borderRadius: spacing[0.75],
    backgroundColor: palette.ink[300],
  },
  dot1: {
    opacity: 0.4,
  },
  dot2: {
    opacity: 0.7,
  },
  dot3: {
    opacity: 1,
  },
});
