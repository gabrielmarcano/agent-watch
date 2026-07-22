import SwiftUI

struct ConfigView: View {
    @ObservedObject var networkService: AgentNetworkService
    @Environment(\.presentationMode) var presentationMode
    
    var body: some View {
        ScrollView {
            VStack(spacing: 12) {
                VStack(spacing: 2) {
                    Text("SERVER CONFIG").font(.system(size: 11, weight: .heavy)).foregroundColor(.blue)
                    Text("Configure Bridge Host IPs").font(.system(size: 9)).foregroundColor(.gray)
                }
                
                // Local IP Card
                VStack {
                    Text("LOCAL NETWORK IP").font(.system(size: 9, weight: .bold)).foregroundColor(.blue)
                    TextField("IP Address", text: $networkService.localIp)
                        .font(.system(size: 12, weight: .bold))
                        .multilineTextAlignment(.center)
                    Text("Tap to edit").font(.system(size: 8)).foregroundColor(.gray)
                }
                .padding()
                .background(Color.blue.opacity(0.1))
                .cornerRadius(12)
                
                // Tailscale IP Card
                VStack {
                    Text("TAILSCALE MESH IP").font(.system(size: 9, weight: .bold)).foregroundColor(.blue)
                    TextField("IP Address", text: $networkService.tailscaleIp)
                        .font(.system(size: 12, weight: .bold))
                        .multilineTextAlignment(.center)
                    Text("Tap to edit").font(.system(size: 8)).foregroundColor(.gray)
                }
                .padding()
                .background(Color.blue.opacity(0.1))
                .cornerRadius(12)
                
                Button("SAVE & RECONNECT") {
                    networkService.startListening()
                    presentationMode.wrappedValue.dismiss()
                }
                .buttonStyle(.borderedProminent)
                .tint(.blue)
                
                Button("CANCEL") {
                    presentationMode.wrappedValue.dismiss()
                }
                .buttonStyle(.bordered)
                .tint(.gray)
            }
            .padding()
        }
    }
}
