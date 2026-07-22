import SwiftUI

extension Color {
    // Standard Colors
    static let agentGray900 = Color(red: 0x12/255.0, green: 0x12/255.0, blue: 0x12/255.0)
    static let agentDarkGreen = Color(red: 0x0D/255.0, green: 0x5C/255.0, blue: 0x3A/255.0)
    static let agentBrightGreen = Color(red: 0x10/255.0, green: 0xB9/255.0, blue: 0x81/255.0)
    static let agentBrightYellow = Color(red: 0xF5/255.0, green: 0x9E/255.0, blue: 0x0B/255.0)
    static let agentLightBlue = Color(red: 0x3B/255.0, green: 0x82/255.0, blue: 0xF6/255.0)
    
    // UI Specific Colors
    static let agentBrandBlue = Color(red: 0x8A/255.0, green: 0xB4/255.0, blue: 0xF8/255.0) // 0xFF8AB4F8
    static let agentRed = Color(red: 0xFF/255.0, green: 0x3B/255.0, blue: 0x30/255.0)
    static let agentGoogleBlue = Color(red: 0x42/255.0, green: 0x85/255.0, blue: 0xF4/255.0) // 0xFF4285F4
    
    // Backgrounds
    static let agentCardBg = Color.white.opacity(0.1)
    static let agentCardBorder = Color.white.opacity(0.2)
}
