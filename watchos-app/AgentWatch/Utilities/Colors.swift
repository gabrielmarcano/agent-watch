import SwiftUI

extension Color {
    // Standard Colors adjusted for better OLED contrast
    static let agentGray900 = Color(white: 0.15)
    static let agentDarkGreen = Color(red: 0x0D/255.0, green: 0x5C/255.0, blue: 0x3A/255.0)
    
    // Brighter greens and yellows for the tiny screen
    static let agentBrightGreen = Color(red: 0x34/255.0, green: 0xD3/255.0, blue: 0x99/255.0) 
    static let agentBrightYellow = Color(red: 0xFB/255.0, green: 0xBF/255.0, blue: 0x24/255.0) 
    static let agentLightBlue = Color(red: 0x60/255.0, green: 0xA5/255.0, blue: 0xFA/255.0)
    
    // UI Specific Colors - Lighter shades to pop on black
    static let agentBrandBlue = Color(red: 0xA8/255.0, green: 0xC7/255.0, blue: 0xFA/255.0)
    static let agentRed = Color(red: 0xFF/255.0, green: 0x45/255.0, blue: 0x3A/255.0)
    static let agentGoogleBlue = Color(red: 0x42/255.0, green: 0x85/255.0, blue: 0xF4/255.0)
    
    // Backgrounds adjusted for 40mm OLED
    static let agentCardBg = Color(white: 0.12)
    static let agentCardBorder = Color.white.opacity(0.3) // Higher opacity for distinct separation
}
