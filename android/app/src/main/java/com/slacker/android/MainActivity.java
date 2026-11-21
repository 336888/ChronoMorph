package com.slacker.android;

import android.app.Activity;
import android.os.Bundle;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;
import org.json.JSONObject;
import java.nio.charset.StandardCharsets;
import android.util.Base64;
import java.util.ArrayList;

public final class MainActivity extends Activity {
    private final ArrayList<String> nodes = new ArrayList<>();
    private LinearLayout list;
    private TextView count;

    private GradientDrawable bg(int color, int radius) {
        GradientDrawable d = new GradientDrawable();
        d.setColor(color); d.setCornerRadius(radius); return d;
    }
    private TextView label(String text, int size, int color, boolean bold) {
        TextView v = new TextView(this);
        v.setText(text); v.setTextSize(size); v.setTextColor(color);
        if (bold) v.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        return v;
    }
    private void pad(View v, int top) {
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(-1,-2);
        p.topMargin=top; v.setLayoutParams(p);
    }
    private String decodeNode(String input) throws Exception {
        String s=input.trim();
        int pos=s.indexOf("://");
        if(pos<0 || !s.startsWith("cm")) throw new IllegalArgumentException("只支持 Slacker cm:// 节点");
        String payload=s.substring(pos+3).trim().replace('-','+').replace('_','/');
        byte[] bytes=Base64.decode(payload, Base64.DEFAULT);
        JSONObject node=new JSONObject(new String(bytes, StandardCharsets.UTF_8));
        String host=node.optString("s"), key=node.optString("k");
        if(host.isEmpty() || key.isEmpty()) throw new IllegalArgumentException("节点缺少地址或密钥");
        return node.optString("n",host)+" · "+s.substring(0,pos);
    }
    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        int dark=Color.rgb(20,25,37), sub=Color.rgb(113,124,143), blue=Color.rgb(41,111,244);
        ScrollView scroll=new ScrollView(this); scroll.setFillViewport(true);
        LinearLayout root=new LinearLayout(this); root.setOrientation(1);
        root.setPadding(44,60,44,40); root.setBackgroundColor(Color.rgb(247,249,253));
        scroll.addView(root); setContentView(scroll);
        TextView title=label("Slacker",34,dark,true); root.addView(title);
        TextView tagline=label("Android · Pixel Edition",14,sub,false); pad(tagline,4);root.addView(tagline);
        LinearLayout panel=new LinearLayout(this);panel.setOrientation(1);
        panel.setPadding(30,32,30,32);panel.setBackground(bg(Color.WHITE,32));pad(panel,35);root.addView(panel);
        TextView stateText=label("尚未连接",24,dark,true);panel.addView(stateText);
        TextView description=label("VPN 数据转发核心尚未接入。本预览版不会接管或中断系统网络。",14,sub,false);
        pad(description,12);panel.addView(description);
        Button connect=new Button(this);connect.setText("连接功能开发中");connect.setAllCaps(false);
        connect.setEnabled(false);pad(connect,22);panel.addView(connect);
        TextView header=label("我的节点",22,dark,true);pad(header,38);root.addView(header);
        count=label("0 个节点 · 仅本次会话",13,sub,false);pad(count,6);root.addView(count);
        EditText input=new EditText(this);input.setHint("粘贴 cm:// / cm+reality:// 等节点链接");
        input.setSingleLine(false);input.setMinLines(2);input.setTextSize(14);
        input.setPadding(20,18,20,18);input.setBackground(bg(Color.WHITE,24));pad(input,18);root.addView(input);
        Button add=new Button(this);add.setText("导入节点");add.setTextColor(Color.WHITE);
        add.setBackgroundTintList(android.content.res.ColorStateList.valueOf(blue));
        pad(add,14);root.addView(add);
        list=new LinearLayout(this);list.setOrientation(1);pad(list,14);root.addView(list);
        add.setOnClickListener(v->{
            try {
                String name=decodeNode(input.getText().toString());
                nodes.add(name);count.setText(nodes.size()+" 个节点 · 仅本次会话");
                TextView item=label("●  "+name,15,dark,true);
                item.setPadding(24,22,24,22);item.setBackground(bg(Color.WHITE,24));pad(item,10);list.addView(item);
                input.setText("");Toast.makeText(this,"节点格式已解析（未连接）",Toast.LENGTH_SHORT).show();
            } catch(Exception e) {Toast.makeText(this,e.getMessage(),Toast.LENGTH_LONG).show();}
        });
        TextView note=label("预览版本：节点导入和界面可用；REALITY、UDP、TUN、链式代理与智能分流尚未实现。请勿用于实际 VPN 连接。",13,sub,false);
        pad(note,32);root.addView(note);
    }
}
