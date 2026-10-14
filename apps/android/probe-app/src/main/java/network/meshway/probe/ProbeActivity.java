package network.meshway.probe;

import android.app.Activity;
import android.os.Bundle;
import android.widget.TextView;
import java.io.File;
import java.io.FileOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.PrintStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.Socket;
import javax.net.ssl.SSLSocket;
import javax.net.ssl.SSLSocketFactory;
import javax.net.ssl.SSLParameters;
import org.json.JSONObject;

/** Disposable separate-UID app: its sockets traverse Meshway's real TUN. */
public final class ProbeActivity extends Activity {
    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        TextView view = new TextView(this); view.setText("Running kernel VPN network probe…"); setContentView(view);
        new Thread(() -> {
            JSONObject result = new JSONObject();
            try {
                String kind = getIntent().getStringExtra("kind"), host = getIntent().getStringExtra("host"), ip = getIntent().getStringExtra("ip");
                int port = getIntent().getIntExtra("port", 443);
                if ("dns".equals(kind)) {
                    InetAddress[] values = InetAddress.getAllByName(host);
                    if (values.length == 0) throw new AssertionError("No DNS addresses");
                    result.put("addresses", values.length);
                } else if ("tcp".equals(kind) || "tls".equals(kind)) {
                    Socket transport = new Socket();
                    transport.connect(new InetSocketAddress(InetAddress.getByName(ip), port), 5000);
                    transport.setSoTimeout(5000);
                    Socket connection = transport;
                    if ("tls".equals(kind)) {
                        SSLSocket tls = (SSLSocket)((SSLSocketFactory)SSLSocketFactory.getDefault()).createSocket(transport,host,port,true);
                        SSLParameters params=tls.getSSLParameters();params.setEndpointIdentificationAlgorithm("HTTPS");tls.setSSLParameters(params);tls.startHandshake();connection=tls;
                        result.put("tls", tls.getSession().getProtocol());
                    }
                    connection.getOutputStream().write(("GET / HTTP/1.0\r\nHost: "+host+"\r\nConnection: close\r\n\r\n").getBytes("UTF-8"));
                    byte[] data = new byte[128]; ByteArrayOutputStream prefix=new ByteArrayOutputStream();
                    while(prefix.size()<5){int n=connection.getInputStream().read(data);if(n<0)break;prefix.write(data,0,n);}
                    if(!prefix.toString("UTF-8").startsWith("HTTP/"))throw new AssertionError("No actual HTTP response (head="+prefix.toString("UTF-8")+")");
                    result.put("response_bytes",prefix.size());connection.close();
                } else throw new IllegalArgumentException("Unknown probe");
                result.put("success",true);
            } catch(Throwable error) { try { result.put("success",false);result.put("error",error.getClass().getSimpleName()+": "+error.getMessage()); } catch(Exception ignored) {} }
            try(FileOutputStream output=new FileOutputStream(new File(getFilesDir(),"result.json"))){ output.write(result.toString().getBytes("UTF-8")); }
            catch(Exception ignored) {}
            runOnUiThread(() -> view.setText(result.toString()));
        }, "Meshway-isolated-network-probe").start();
    }
}
