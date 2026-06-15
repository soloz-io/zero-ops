import socket
import threading
import sys

def forward(src, dst):
    while True:
        try:
            data = src.recv(8192)
            if not data:
                break
            dst.sendall(data)
        except Exception:
            break
    try:
        src.close()
    except:
        pass
    try:
        dst.close()
    except:
        pass

def accept_loop(local_port, target_ip, target_port):
    server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        server.bind(('0.0.0.0', local_port))
    except Exception as e:
        print(f"Failed to bind: {e}")
        sys.exit(1)
        
    server.listen(100)
    print(f"🚀 Bridge active: host.docker.internal:{local_port} -> {target_ip}:{target_port}")
    
    while True:
        try:
            client, _ = server.accept()
            target = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            target.connect((target_ip, target_port))
            
            threading.Thread(target=forward, args=(client, target), daemon=True).start()
            threading.Thread(target=forward, args=(target, client), daemon=True).start()
        except Exception as e:
            try:
                client.close()
            except:
                pass

if __name__ == '__main__':
    # Usage: python3 mac-windows-proxy.py <local_port> <target_ip> <target_port>
    if len(sys.argv) != 4:
        print("Usage: python3 mac-windows-proxy.py <local_port> <target_ip> <target_port>")
        sys.exit(1)
        
    accept_loop(int(sys.argv[1]), sys.argv[2], int(sys.argv[3]))
