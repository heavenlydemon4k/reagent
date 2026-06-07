// Decision Stack — Chat Input Bar
// Multi-line text input with voice button, send button, and slash command support

import React, { useState, useCallback, useMemo } from 'react';
import {
  View,
  Text,
  TextInput,
  TouchableOpacity,
  StyleSheet,
  Keyboard,
  ScrollView,
} from 'react-native';
import { palette } from '@theme/colors';
import { fontSize, fontWeight } from '@theme/typography';
import { spacing } from '@theme/spacing';
import { VoiceInputButton } from './VoiceInputButton';
import { VoiceWaveform } from '../voice/VoiceWaveform';

export type InputMode = 'text' | 'voice';

export type SlashCommand = 'calendar' | 'send' | 'freebusy' | 'help';

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
  onSlashCommand?: (command: SlashCommand, args: string) => void;
}

/**
 * ChatInput — footer input bar with text entry, voice toggle, and send.
 * Switches between text input mode and voice recording mode.
 */
const SLASH_COMMANDS: { command: SlashCommand; label: string; description: string }[] = [
  { command: 'calendar', label: '/calendar', description: 'Check your calendar events' },
  { command: 'freebusy', label: '/freebusy', description: 'Check free/busy for a date' },
  { command: 'send', label: '/send', description: 'Send an approved draft' },
  { command: 'help', label: '/help', description: 'Show available commands' },
];

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
  onSlashCommand,
}) => {
  const [isFocused, setIsFocused] = useState(false);
  const inputRef = React.useRef<TextInput>(null);

  const canSend = value.trim().length > 0 && !isLoading;

  // Detect active slash command for suggestion UI
  const activeSlashCommand = useMemo((): {
    command: SlashCommand;
    label: string;
    description: string;
  } | null => {
    if (!value.startsWith('/')) return null;
    const cmd = value.slice(1).split(' ')[0].toLowerCase();
    const match = SLASH_COMMANDS.find((c) => c.command === cmd);
    return match ?? null;
  }, [value]);

  // Show command suggestions when typing "/"
  const showCommandSuggestions = value.startsWith('/') && !value.includes(' ');
  const commandSuggestions = useMemo(() => {
    if (!showCommandSuggestions) return [];
    const partial = value.slice(1).toLowerCase();
    return SLASH_COMMANDS.filter((c) => c.command.startsWith(partial));
  }, [showCommandSuggestions, value]);

  const handleSend = useCallback(() => {
    const trimmed = value.trim();
    if (!trimmed || isLoading) return;

    // Route slash commands to handler
    if (trimmed.startsWith('/') && onSlashCommand) {
      const parts = trimmed.slice(1).split(' ');
      const cmd = parts[0] as SlashCommand;
      const args = parts.slice(1).join(' ');
      if (SLASH_COMMANDS.some((c) => c.command === cmd)) {
        onSlashCommand(cmd, args);
        onChangeText('');
        Keyboard.dismiss();
        return;
      }
    }

    if (canSend) {
      onSend(trimmed);
      onChangeText('');
      Keyboard.dismiss();
    }
  }, [canSend, value, isLoading, onSend, onChangeText, onSlashCommand]);

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

  const handleSelectCommand = useCallback((cmd: string) => {
    onChangeText(`/${cmd} `);
    // Focus back on input after selecting
    setTimeout(() => inputRef.current?.focus(), 50);
  }, [onChangeText]);

  return (
    <View style={styles.container}>
      {/* Voice mode: show waveform */}
      {mode === 'voice' && isRecording && (
        <View style={styles.waveformContainer}>
          <VoiceWaveform amplitude={waveformAmplitude} isActive={isRecording} />
        </View>
      )}

      {/* Slash command suggestions */}
      {showCommandSuggestions && commandSuggestions.length > 0 && (
        <View style={styles.suggestionsContainer}>
          <ScrollView
            horizontal
            showsHorizontalScrollIndicator={false}
            keyboardShouldPersistTaps="handled"
            contentContainerStyle={styles.suggestionsScroll}
          >
            {commandSuggestions.map((suggestion) => (
              <TouchableOpacity
                key={suggestion.command}
                style={styles.suggestionChip}
                onPress={() => handleSelectCommand(suggestion.command)}
                activeOpacity={0.7}
              >
                <Text style={styles.suggestionLabel}>{suggestion.label}</Text>
                <Text style={styles.suggestionDesc}>{suggestion.description}</Text>
              </TouchableOpacity>
            ))}
          </ScrollView>
        </View>
      )}

      {/* Active slash command indicator */}
      {activeSlashCommand && (
        <View style={styles.activeCommandBar}>
          <Text style={styles.activeCommandText}>
            {activeSlashCommand.label} — {activeSlashCommand.description}
          </Text>
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
  suggestionsContainer: {
    paddingVertical: spacing[2],
    marginBottom: spacing[2],
    borderBottomWidth: 1,
    borderBottomColor: palette.ink[100],
  },
  suggestionsScroll: {
    flexDirection: 'row',
    gap: spacing[2],
  },
  suggestionChip: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: palette.ink[50],
    borderRadius: spacing[4],
    paddingHorizontal: spacing[3],
    paddingVertical: spacing[1.5],
    borderWidth: 1,
    borderColor: palette.ink[200],
    gap: spacing[1.5],
  },
  suggestionLabel: {
    fontSize: fontSize.sm,
    fontWeight: fontWeight.semibold,
    color: palette.sand[500],
  },
  suggestionDesc: {
    fontSize: fontSize.xs,
    color: palette.ink[500],
  },
  activeCommandBar: {
    paddingHorizontal: spacing[3],
    paddingVertical: spacing[1],
    marginBottom: spacing[2],
    backgroundColor: palette.sand[50],
    borderRadius: spacing[2],
    borderLeftWidth: 3,
    borderLeftColor: palette.sand[400],
  },
  activeCommandText: {
    fontSize: fontSize.xs,
    color: palette.ink[600],
    fontWeight: fontWeight.medium,
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
