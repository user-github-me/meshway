import android.os.SystemClock;
import java.net.InetAddress;
import java.net.Socket;
import java.net.InetSocketAddress;
import java.io.OutputStream;
import java.io.InputStream;
import javax.net.ssl.SSLSocket;
import javax.net.ssl.SSLSocketFactory;
import javax.net.ssl.SSLParameters;

/** Runs as Android shell UID, which is routed through the real VpnService TUN. */
public final class NetworkProbe {
    public static void main(String[] args) throws Exception {
        if (args.length == 0) throw new IllegalArgumentException("Expected dns, tcp, or tls probe");
        long begin = SystemClock.elapsedRealtime();
        if (args[0].equals("dns")) {
            InetAddress[] values = InetAddress.getAllByName(args[1]);
            if (values.length == 0) throw new AssertionError("DNS returned no addresses");
            System.out.println("DNS_OK " + values.length + " " + (SystemClock.elapsedRealtime() - begin));
        } else if (args[0].equals("tcp")) {
            try (Socket socket = new Socket()) {
                socket.connect(new InetSocketAddress(args[1], Integer.parseInt(args[2])), 5000);
                socket.setSoTimeout(5000);
                socket.getOutputStream().write("GET / HTTP/1.0\r\nHost: probe\r\n\r\n".getBytes("UTF-8"));
                byte[] buf = new byte[128];
                if (socket.getInputStream().read(buf) < 1) throw new AssertionError("No TCP response");
                System.out.println("TCP_OK " + (SystemClock.elapsedRealtime() - begin));
            }
        } else if (args[0].equals("tls")) {
            String hostname = args[1];
            InetAddress address = InetAddress.getByName(args[2]);
            Socket transport = new Socket();
            transport.connect(new InetSocketAddress(address, 443), 10000);
            SSLSocket socket = (SSLSocket)((SSLSocketFactory)SSLSocketFactory.getDefault()).createSocket(transport, hostname, 443, true);
            socket.setSoTimeout(10000);
            SSLParameters parameters = socket.getSSLParameters(); parameters.setEndpointIdentificationAlgorithm("HTTPS"); socket.setSSLParameters(parameters);
            socket.startHandshake();
            socket.getOutputStream().write(("GET / HTTP/1.0\r\nHost: " + hostname + "\r\nConnection: close\r\n\r\n").getBytes("UTF-8"));
            byte[] buf = new byte[128]; int n = socket.getInputStream().read(buf);
            if (n < 1 || !new String(buf, 0, n, "UTF-8").startsWith("HTTP/")) throw new AssertionError("No HTTPS response");
            System.out.println("TLS_OK " + socket.getSession().getProtocol() + " " + (SystemClock.elapsedRealtime() - begin));
            socket.close();
        } else throw new IllegalArgumentException("Unknown probe");
    }
}
