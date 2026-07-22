import SwiftUI

struct ContentView: View {
    @StateObject private var networkService = AgentNetworkService()
    
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 8) {
                    // Header Brand
                    headerView
                    
                    // Folder Badge
                    if let cwd = networkService.state.cwd, !cwd.isEmpty {
                        Text("DIR / \((cwd as NSString).lastPathComponent)")
                            .font(.system(size: 10, weight: .bold))
                            .foregroundColor(.agentBrandBlue)
                            .padding(.horizontal, 10).padding(.vertical, 4)
                            .background(Color.agentBrandBlue.opacity(0.2))
                            .cornerRadius(12)
                            .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color.agentBrandBlue.opacity(0.3), lineWidth: 1))
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
                            .scaleEffect(1.5)
                            .padding(.vertical, 8)
                    }
                    
                    // Actions
                    VStack(spacing: 6) {
                        Button(action: {
                            // dictation action to be implemented
                        }) {
                            HStack {
                                Image(systemName: "mic.fill")
                                Text("VOICE").bold()
                            }
                            .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(.agentBrandBlue)
                        
                        NavigationLink(destination: HistoryListView(state: networkService.state)) {
                            HStack {
                                Image(systemName: "clock.fill")
                                Text("HISTORY").bold()
                            }
                            .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.bordered)
                        .tint(.white)
                    }
                    .padding(.top, 4)
                    
                    // Config Link
                    NavigationLink(destination: ConfigView(networkService: networkService)) {
                        VStack(spacing: 2) {
                            Text("SERVER CONNECTION").font(.system(size: 8, weight: .bold)).foregroundColor(.gray)
                            Text(networkService.localIp).font(.system(size: 10, weight: .bold)).foregroundColor(.white)
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                    .tint(.agentCardBg)
                    .padding(.top, 4)
                }
                .padding()
            }
            .background(Color.black.edgesIgnoringSafeArea(.all)) // Match Wear OS pure black
        }
        .onAppear {
            networkService.startListening()
        }
    }
    
    // MARK: - Subviews
    private var headerView: some View {
        HStack(spacing: 6) {
            Image(systemName: "greaterthan.square.fill")
                .foregroundColor(.agentBrandBlue)
            Text("AGENT WATCH")
                .font(.system(size: 12, weight: .heavy, design: .monospaced))
                .foregroundColor(.white)
        }
        .padding(.horizontal, 10).padding(.vertical, 6)
        .background(Color.agentBrandBlue.opacity(0.2))
        .cornerRadius(8)
        .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.agentBrandBlue.opacity(0.3), lineWidth: 1))
    }
    
    private var statusBadge: some View {
        HStack(spacing: 10) {
            Circle()
                .fill(statusColor)
                .frame(width: 8, height: 8)
                .shadow(color: statusColor, radius: 4)
            
            VStack(alignment: .leading, spacing: 2) {
                Text(statusTitle).font(.system(size: 11, weight: .bold)).foregroundColor(statusColor)
                Text(statusSubtitle).font(.system(size: 9)).foregroundColor(.gray)
            }
            Spacer()
        }
        .padding()
        .background(Color.agentCardBg)
        .cornerRadius(12)
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color.agentCardBorder, lineWidth: 1))
    }
    
    private var authCard: some View {
        VStack(spacing: 8) {
            Text("PERMISSION REQUIRED").font(.system(size: 9, weight: .black)).foregroundColor(.agentRed)
            Text(networkService.state.tool_name ?? "Unknown tool").font(.system(size: 12, weight: .bold)).foregroundColor(.white)
            
            if let cmd = networkService.state.commandToApprove {
                Text(cmd).font(.system(size: 9, design: .monospaced)).foregroundColor(.gray).lineLimit(3).multilineTextAlignment(.center)
            }
            
            HStack {
                Button(action: { networkService.sendInputCommand(text: "n") }) {
                    Image(systemName: "xmark")
                }
                .buttonStyle(.borderedProminent)
                .tint(.agentRed)
                
                Button(action: { networkService.sendInputCommand(text: "y") }) {
                    Image(systemName: "checkmark")
                }
                .buttonStyle(.borderedProminent)
                .tint(.agentBrightGreen)
            }
        }
        .padding()
        .background(Color.agentRed.opacity(0.15))
        .cornerRadius(16)
        .overlay(RoundedRectangle(cornerRadius: 16).stroke(Color.agentRed.opacity(0.3), lineWidth: 1))
    }
    
    // MARK: - Computed Properties
    private var statusColor: Color {
        switch networkService.state.status {
        case "thinking": return .agentBrightYellow
        case "waiting_for_permission": return .agentRed
        case "done": return .agentBrightGreen
        default: return .gray
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
        case "waiting_for_permission": return "Action required"
        case "done": return "Task completed"
        default: return "Waiting for prompt"
        }
    }
}
