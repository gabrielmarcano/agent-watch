import SwiftUI

struct ContentView: View {
    @StateObject private var networkService = AgentNetworkService()
    
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 6) { // Reduced spacing to fit 40mm screen
                    
                    // Header Brand
                    headerView
                    
                    // Folder Badge
                    if let cwd = networkService.state.cwd, !cwd.isEmpty {
                        Text("DIR / \((cwd as NSString).lastPathComponent)")
                            .font(.system(size: 9, weight: .bold)) // Slightly smaller
                            .foregroundColor(.black) // High contrast against blue background
                            .padding(.horizontal, 8).padding(.vertical, 3)
                            .background(Color.agentBrandBlue) // Solid bright background
                            .cornerRadius(8)
                            .lineLimit(1)
                            .minimumScaleFactor(0.8)
                    }
                    
                    // Status Badge Indicator
                    statusBadge
                    
                    // Permission Request Card
                    if networkService.state.status == "waiting_for_permission" {
                        authCard
                    }
                    
                    // Thinking Indicator
                    if networkService.state.status == "thinking" {
                        ProgressView()
                            .progressViewStyle(CircularProgressViewStyle(tint: .agentBrightYellow))
                            .scaleEffect(1.2) // Adjusted scale for smaller screens
                            .padding(.vertical, 4)
                    }
                    
                    // Actions
                    VStack(spacing: 4) { // Tighter spacing
                        Button(action: {
                            // dictation action
                        }) {
                            HStack {
                                Image(systemName: "mic.fill")
                                Text("VOICE").bold()
                            }
                            .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(.agentBrandBlue)
                        .foregroundColor(.black) // Best contrast on light blue
                        
                        NavigationLink(destination: HistoryListView(state: networkService.state)) {
                            HStack {
                                Image(systemName: "clock.fill")
                                Text("HISTORY").bold()
                            }
                            .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.bordered)
                        .tint(.gray)
                        .foregroundColor(.white)
                    }
                    .padding(.top, 2)
                    
                    // Config Link
                    NavigationLink(destination: ConfigView(networkService: networkService)) {
                        VStack(spacing: 1) {
                            Text("SERVER").font(.system(size: 8, weight: .semibold)).foregroundColor(Color.white.opacity(0.7))
                            Text(networkService.localIp).font(.system(size: 10, weight: .bold)).foregroundColor(.white)
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                    .tint(.agentCardBg)
                    .padding(.top, 2)
                }
                .padding(.horizontal, 6) // Maximize horizontal screen real estate for 40mm
                .padding(.bottom, 12)
            }
        }
        .onAppear {
            networkService.startListening()
        }
    }
    
    // MARK: - Subviews
    private var headerView: some View {
        HStack(spacing: 4) {
            Image(systemName: "greaterthan.square.fill")
                .foregroundColor(.agentBrandBlue)
                .font(.system(size: 11))
            Text("AGENT WATCH")
                .font(.system(size: 11, weight: .heavy, design: .monospaced))
                .foregroundColor(.white)
                .lineLimit(1)
                .minimumScaleFactor(0.8)
        }
        .padding(.horizontal, 8).padding(.vertical, 4)
        .background(Color.agentBrandBlue.opacity(0.2))
        .cornerRadius(6)
        .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.agentBrandBlue.opacity(0.4), lineWidth: 1))
    }
    
    private var statusBadge: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(statusColor)
                .frame(width: 8, height: 8)
                .shadow(color: statusColor, radius: 3)
            
            VStack(alignment: .leading, spacing: 1) {
                Text(statusTitle)
                    .font(.system(size: 10, weight: .bold))
                    .foregroundColor(statusColor)
                    .lineLimit(1)
                    .minimumScaleFactor(0.8)
                Text(statusSubtitle)
                    .font(.system(size: 8))
                    .foregroundColor(Color.white.opacity(0.8)) // Brighter than the old gray
                    .lineLimit(1)
            }
            Spacer()
        }
        .padding(8) // Reduced from default padding
        .background(Color.agentCardBg)
        .cornerRadius(10)
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.agentCardBorder, lineWidth: 1))
    }
    
    private var authCard: some View {
        VStack(spacing: 6) {
            Text("ACTION REQUIRED")
                .font(.system(size: 9, weight: .black))
                .foregroundColor(.agentRed)
            
            Text(networkService.state.tool_name ?? "Unknown tool")
                .font(.system(size: 11, weight: .bold))
                .foregroundColor(.white)
                .lineLimit(1)
                .minimumScaleFactor(0.8)
            
            if let cmd = networkService.state.commandToApprove {
                Text(cmd)
                    .font(.system(size: 9, design: .monospaced))
                    .foregroundColor(Color.white.opacity(0.8))
                    .lineLimit(2)
                    .multilineTextAlignment(.center)
            }
            
            HStack(spacing: 8) {
                Button(action: { networkService.sendInputCommand(text: "n") }) {
                    Image(systemName: "xmark")
                }
                .buttonStyle(.borderedProminent)
                .tint(.agentRed)
                .foregroundColor(.white)
                
                Button(action: { networkService.sendInputCommand(text: "y") }) {
                    Image(systemName: "checkmark")
                }
                .buttonStyle(.borderedProminent)
                .tint(.agentBrightGreen)
                .foregroundColor(.black) // Better contrast for the green background
            }
        }
        .padding(8)
        .background(Color.agentRed.opacity(0.2))
        .cornerRadius(12)
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color.agentRed.opacity(0.5), lineWidth: 1))
    }
    
    // MARK: - Computed Properties
    private var statusColor: Color {
        switch networkService.state.status {
        case "thinking": return .agentBrightYellow
        case "waiting_for_permission": return .agentRed
        case "done": return .agentBrightGreen
        default: return .white.opacity(0.6)
        }
    }
    
    private var statusTitle: String {
        switch networkService.state.status {
        case "thinking": return "THINKING..."
        case "waiting_for_permission": return "NEEDS AUTH"
        case "done": return "READY"
        default: return "IDLE"
        }
    }
    
    private var statusSubtitle: String {
        switch networkService.state.status {
        case "thinking": return "Processing query"
        case "waiting_for_permission": return "Permission required"
        case "done": return "Task completed"
        default: return "Waiting for prompt"
        }
    }
}
