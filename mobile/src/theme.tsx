import React, { createContext, useContext } from 'react';
import { Pressable, Text, TextInput, View, ViewStyle } from 'react-native';

export type Mode = 'dark' | 'light';
export type Palette = {
  mode: Mode; bg: string; surface: string; raised: string; field: string;
  border: string; text: string; muted: string; red: string; redText: string;
  onRed: string; dangerBg: string;
};
export const palettes: Record<Mode, Palette> = {
  dark: { mode: 'dark', bg: '#101217', surface: '#1a1d24', raised: '#242832',
    field: '#14171d', border: '#343943', text: '#f5f2ef', muted: '#adb3bd',
    red: '#e5484d', redText: '#ff7378', onRed: '#090a0d', dangerBg: '#3d1a20' },
  light: { mode: 'light', bg: '#f5f5f7', surface: '#ffffff', raised: '#ebeef2',
    field: '#ffffff', border: '#d5d9e0', text: '#1b2028', muted: '#59616d',
    red: '#e5484d', redText: '#b4232d', onRed: '#090a0d', dangerBg: '#ffe5e7' },
};
const ThemeContext = createContext<Palette>(palettes.dark);
export const ThemeProvider = ThemeContext.Provider;
export const usePalette = () => useContext(ThemeContext);

export function Label({ children, muted = false, size = 14, bold = false,
  style }: { children: React.ReactNode; muted?: boolean; size?: number; bold?: boolean; style?: object }) {
  const p = usePalette();
  return <Text style={[{ color: muted ? p.muted : p.text, fontSize: size,
    fontWeight: bold ? '700' : '400' }, style]}>{children}</Text>;
}

export function Panel({ children, style }: { children: React.ReactNode; style?: ViewStyle }) {
  const p = usePalette();
  return <View style={[{ backgroundColor: p.surface, borderColor: p.border,
    borderWidth: 1, borderRadius: 16, padding: 16 }, style]}>{children}</View>;
}

export function Action({ title, onPress, secondary = false, danger = false, disabled = false,
  small = false, testID }: { title: string; onPress: () => void; secondary?: boolean;
  danger?: boolean; disabled?: boolean; small?: boolean; testID?: string }) {
  const p = usePalette();
  return <Pressable testID={testID} accessibilityRole="button" accessibilityLabel={title}
    disabled={disabled} onPress={onPress}
    style={({ pressed }) => ({ backgroundColor: secondary ? p.raised : danger ? p.dangerBg : p.red,
      borderColor: danger ? p.red : secondary ? p.border : p.red, borderWidth: 1,
      borderRadius: 10, paddingHorizontal: small ? 11 : 16, paddingVertical: small ? 8 : 12,
      opacity: disabled ? .4 : pressed ? .7 : 1, alignItems: 'center', justifyContent: 'center' })}>
    <Text style={{ color: secondary ? p.text : danger ? p.redText : p.onRed,
      fontWeight: '700', fontSize: small ? 12 : 14 }}>{title}</Text>
  </Pressable>;
}

export function Field({ label, value, onChangeText, placeholder, secureTextEntry = false,
  multiline = false, keyboardType, autoCapitalize = 'none' }: {
  label: string; value: string; onChangeText: (value: string) => void; placeholder?: string;
  secureTextEntry?: boolean; multiline?: boolean; keyboardType?: 'default' | 'email-address' | 'numeric' | 'url';
  autoCapitalize?: 'none' | 'sentences' | 'words' | 'characters';
}) {
  const p = usePalette();
  return <View style={{ marginBottom: 14 }}>
    <Label muted size={11} bold style={{ marginBottom: 7, letterSpacing: 1 }}>{label.toUpperCase()}</Label>
    <TextInput accessibilityLabel={label} value={value} onChangeText={onChangeText}
      placeholder={placeholder} placeholderTextColor={p.muted} secureTextEntry={secureTextEntry}
      multiline={multiline} keyboardType={keyboardType} autoCapitalize={autoCapitalize}
      selectionColor={p.red} style={{ backgroundColor: p.field, borderColor: p.border,
        borderWidth: 1, borderRadius: 10, padding: 12, color: p.text,
        fontSize: 15, minHeight: multiline ? 94 : 46, textAlignVertical: multiline ? 'top' : 'center' }} />
  </View>;
}

export function Chip({ title, selected, onPress }: { title: string; selected: boolean; onPress: () => void }) {
  const p = usePalette();
  return <Pressable accessibilityRole="button" accessibilityState={{ selected }} onPress={onPress}
    style={{ paddingHorizontal: 12, paddingVertical: 9, borderRadius: 16, margin: 3,
      backgroundColor: selected ? p.dangerBg : p.raised,
      borderColor: selected ? p.red : p.border, borderWidth: 1 }}>
    <Label size={12} bold>{title}</Label>
  </Pressable>;
}

export function ErrorText({ message }: { message: string | null }) {
  const p = usePalette();
  return message ? <Text accessibilityRole="alert" style={{ color: p.redText,
    backgroundColor: p.dangerBg, padding: 10, borderRadius: 9, marginVertical: 8 }}>
    Error: {message}
  </Text> : null;
}
