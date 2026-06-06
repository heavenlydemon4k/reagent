// Decision Stack — Message Bubble
// User (right-aligned, warm accent) and assistant (left-aligned, light) message bubbles

import React, { useState } from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
} from 'react-native';
import { palette } from '@theme/colors';
import { fontSize, fontWeight, lineHeight } from '@theme/typography';
import { spacing } from '@theme/spacing';
import type { ChatMessage, ChunkCitation } from '@types/cards';
import { CitationInline } from './CitationInline';
import { VoicePlayback } from '../voice/VoicePlayback';

interface MessageBubbleProps {
  message: ChatMessage;
  onPressCitation: (citation: ChunkCitation) => void;
  onPlayAudio?: (audioUrl: string) => void;
  isPlayingAudio?: boolean;
}

/**
 * MessageBubble — renders a single chat message with optional citations and audio.
 * User messages: right-aligned with warm sand accent background.
 * Assistant messages: left-aligned with white/light background.
 */
export const MessageBubble: React.FC<MessageBubbleProps> = ({
  message,
  onPressCitation,
  onPlayAudio,
  isPlayingAudio = false,
}) => {
  const isUser = message.role === 'user';
  const [showTimestamp, setShowTimestamp] = useState(false);

  const handlePress = () => {
    setShowTimestamp((prev) => !prev);
  };

  const formattedTime = new Date(message.created_at).toLocaleTimeString(undefined, {
    hour: 'numeric',
    minute: '2-digit',
  });

  return (
    <View
      style={[
        styles.container,
        isUser ? styles.userContainer : styles.assistantContainer,
      ]}
    >
      {/* Assistant avatar indicator */}
      {!isUser && (
        <View style={styles.assistantIndicator}>
          <Text style={styles.assistantIndicatorText}>DS</Text>
        </View>
      )}

      <View style={styles.bubbleColumn}>
        {/* Bubble */}
        <TouchableOpacity
          onPress={handlePress}
          activeOpacity={0.8}
          accessibilityLabel={`${isUser ? 'You' : 'Assistant'}: ${message.content}`}
          accessibilityRole="text"
        >
          <View
            style={[
              styles.bubble,
              isUser ? styles.userBubble : styles.assistantBubble,
            ]}
          >
            <Text
              style={[
                styles.messageText,
                isUser ? styles.userMessageText : styles.assistantMessageText,
              ]}
            >
              {message.content}
            </Text>

            {/* Voice audio indicator for assistant messages */}
            {!isUser && message.audio_url && (
              <VoicePlayback
                audioUrl={message.audio_url}
                isPlaying={isPlayingAudio}
                onPlay={() => onPlayAudio?.(message.audio_url!)}
              />
            )}
          </View>
        </TouchableOpacity>

        {/* Citations row (assistant only) */}
        {!isUser && message.citations && message.citations.length > 0 && (
          <View style={styles.citationsRow}>
            <CitationInline
              citations={message.citations}
              onPressCitation={onPressCitation}
            />
          </View>
        )}

        {/* Timestamp (toggle on tap) */}
        {showTimestamp && (
          <Text style={isUser ? styles.userTimestamp : styles.assistantTimestamp}>
            {formattedTime}
          </Text>
        )}
      </View>
    </View>
  );
};

const styles = StyleSheet.create({
  container: {
    flexDirection: 'row',
    marginVertical: spacing[1.5],
    paddingHorizontal: spacing[4],
    maxWidth: '85%',
  },
  userContainer: {
    alignSelf: 'flex-end',
  },
  assistantContainer: {
    alignSelf: 'flex-start',
  },
  assistantIndicator: {
    width: spacing[7],
    height: spacing[7],
    borderRadius: spacing[3.5],
    backgroundColor: palette.sand[100],
    justifyContent: 'center',
    alignItems: 'center',
    marginRight: spacing[2],
    alignSelf: 'flex-end',
    marginBottom: spacing[1],
  },
  assistantIndicatorText: {
    fontSize: fontSize.xs,
    fontWeight: fontWeight.semibold,
    color: palette.sand[600],
  },
  bubbleColumn: {
    flexDirection: 'column',
  },
  bubble: {
    borderRadius: spacing[3],
    paddingHorizontal: spacing[4],
    paddingVertical: spacing[3],
  },
  userBubble: {
    backgroundColor: palette.sand[400],
    borderBottomRightRadius: spacing[1],
  },
  assistantBubble: {
    backgroundColor: '#ffffff',
    borderBottomLeftRadius: spacing[1],
    borderWidth: 1,
    borderColor: palette.ink[100],
  },
  messageText: {
    fontSize: fontSize.base,
    lineHeight: lineHeight.normal * fontSize.base,
  },
  userMessageText: {
    color: '#ffffff',
    fontWeight: fontWeight.medium,
  },
  assistantMessageText: {
    color: palette.ink[900],
    fontWeight: fontWeight.normal,
  },
  citationsRow: {
    marginTop: spacing[1.5],
    marginLeft: spacing[1],
  },
  userTimestamp: {
    fontSize: fontSize.xs,
    color: palette.ink[400],
    textAlign: 'right',
    marginTop: spacing[1],
    marginRight: spacing[1],
  },
  assistantTimestamp: {
    fontSize: fontSize.xs,
    color: palette.ink[400],
    textAlign: 'left',
    marginTop: spacing[1],
    marginLeft: spacing[1],
  },
});
