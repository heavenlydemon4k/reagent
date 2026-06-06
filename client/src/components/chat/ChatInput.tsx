// Decision Stack — Chat Input Bar
// Multi-line text input with voice button and send button

import React, { useState, useCallback } from 'react';
import {
  View,
  Text,
  TextInput,
  TouchableOpacity,
  StyleSheet,
  Keyboard,
} from 'react-native';
import { palette } from '@theme/colors';
import { fontSize, fontWeight } from '@theme/typography';
import { spacing } from '@theme/spacing';
import { VoiceInputButton } from './VoiceInputButton';
import { VoiceWaveform } from '../voice/VoiceWaveform';

export type InputMode = 'text' | 'voice';

interface ChatInputProps {
  value: string;
  onChangeText: (text: string) => void;
  onSend: (text: string) => void;
  isLoading: boolean;
  mode: InputMode;
  onModeChange: (mode: InputMode) => void;
  isRecording: boolean;
  onStartRecording: () => void;
  onStopRecording: () => void;
  waveformAmplitude: number[];
}

/**
 * ChatInput — footer input bar with text entry, voice toggle, and send.
 * Switches between text input mode and voice recording mode.
 */
export const ChatInput: React.FC<ChatInputProps> = ({
  value,
  onChangeText,
  onSend,
  isLoading,
  mode,
  onModeChange,
  isRecording,
  onStartRecording,
  onStopRecording,
  waveformAmplitude,
}) => {
  const [isFocused, setIsFocused] = useState(false);
  const inputRef = React.useRef<TextInput>(null);

  const canSend = value.trim().length > 0 && !isLoading;

  const handleSend = useCallback(() => {
    if (canSend) {
      onSend(value.trim());
      onChangeText('');
      Keyboard.dismiss();
    }
  }, [canSend, value, onSend, onChangeText]);

  const handleVoicePress = useCallback(() => {
    if (mode === 'voice') {
      // Already in voice mode — toggle recording
      if (isRecording) {
        onStopRecording();
      } else {
        onStartRecording();
      }
    } else {
      // Switch to voice mode
      onModeChange('voice');
      Keyboard.dismiss();
      // Auto-start recording after brief transition
      setTimeout(() => {
        onStartRecording();
      }, 300);
    }
  }, [mode, isRecording, onModeChange, onStartRecording, onStopRecording]);

  const handleTextMode = useCallback(() => {
    if (isRecording) {
      onStopRecording();
    }
    onModeChange('text');
    // Focus the text input after switching
    setTimeout(() => {
      inputRef.current?.focus();
    }, 100);
  }, [isRecording, onModeChange, onStopRecording]);

  return (
    <View style={styles.container}>
      {/* Voice mode: show waveform */}
      {mode === 'voice' && isRecording && (
        <View style={styles.waveformContainer}>
          <VoiceWaveform amplitude={waveformAmplitude} isActive={isRecording} />
        </View>
      )}

      <View style={styles.inputRow}>
        {/* Text Input */}
        {mode === 'text' ? (
          <View
            style={[
              styles.inputContainer,
              isFocused && styles.inputContainerFocused,
            ]}
          >
            <TextInput
              ref={inputRef}
              style={styles.textInput}
              value={value}
              onChangeText={onChangeText}
              placeholder="Ask about your email..."
              placeholderTextColor={palette.ink[400]}
              multiline
              maxLength={2000}
              onFocus={() => setIsFocused(true)}
              onBlur={() => setIsFocused(false)}
              returnKeyType="send"
              blurOnSubmit={false}
              onSubmitEditing={handleSend}
              accessibilityLabel="Chat message input"
              accessibilityRole="text"
              editable={!isLoading}
            />
          </View>
        ) : (
          /* Voice mode: placeholder showing recording hint */
          <TouchableOpacity
            style={styles.voicePlaceholder}
            onPress={handleTextMode}
            activeOpacity={0.7}
          >
            <VoiceWaveform
              amplitude={waveformAmplitude}
              isActive={isRecording}
              compact
            />
          </TouchableOpacity>
        )}

        {/* Voice button */}
        <VoiceInputButton
          isRecording={isRecording}
          onPress={handleVoicePress}
          disabled={isLoading}
        />

        {/* Send button (text mode only) */}
        {mode === 'text' && (
          <TouchableOpacity
            onPress={handleSend}
            disabled={!canSend}
            style={[
              styles.sendButton,
              canSend ? styles.sendButtonActive : styles.sendButtonInactive,
            ]}
            activeOpacity={0.7}
            accessibilityLabel="Send message"
            accessibilityRole="button"
          >
            <View style={styles.sendIcon}>
              {/* Arrow icon rendered as text for simplicity */}
              <Text
                style={[
                  styles.sendIconText,
                  canSend && styles.sendIconTextActive,
                ]}
              >
                ↑
              </Text>
            </View>
          </TouchableOpacity>
        )}
      </View>
    </View>
  );
};

const styles = StyleSheet.create({
  container: {
    backgroundColor: '#ffffff',
    borderTopWidth: 1,
    borderTopColor: palette.ink[100],
    paddingHorizontal: spacing[4],
    paddingTop: spacing[2],
    paddingBottom: spacing[4],
  },
  waveformContainer: {
    alignItems: 'center',
    paddingVertical: spacing[3],
    marginBottom: spacing[2],
  },
  inputRow: {
    flexDirection: 'row',
    alignItems: 'flex-end',
    gap: spacing[2],
  },
  inputContainer: {
    flex: 1,
    backgroundColor: palette.ink[50],
    borderRadius: spacing[5],
    borderWidth: 1,
    borderColor: palette.ink[100],
    paddingHorizontal: spacing[4],
    paddingVertical: spacing[2.5],
    maxHeight: spacing[28],
  },
  inputContainerFocused: {
    borderColor: palette.sand[400],
    backgroundColor: '#ffffff',
  },
  textInput: {
    fontSize: fontSize.base,
    color: palette.ink[900],
    lineHeight: 20,
    maxHeight: spacing[24],
    padding: 0,
  },
  voicePlaceholder: {
    flex: 1,
    backgroundColor: palette.ink[50],
    borderRadius: spacing[5],
    borderWidth: 1,
    borderColor: palette.sand[300],
    paddingHorizontal: spacing[4],
    paddingVertical: spacing[2],
    justifyContent: 'center',
  },
  sendButton: {
    width: spacing[10],
    height: spacing[10],
    borderRadius: spacing[5],
    justifyContent: 'center',
    alignItems: 'center',
  },
  sendButtonActive: {
    backgroundColor: palette.sand[400],
  },
  sendButtonInactive: {
    backgroundColor: palette.ink[100],
  },
  sendIcon: {
    justifyContent: 'center',
    alignItems: 'center',
  },
  sendIconText: {
    fontSize: fontSize.lg,
    color: palette.ink[400],
    fontWeight: fontWeight.bold,
  },
  sendIconTextActive: {
    color: '#ffffff',
  },
});
