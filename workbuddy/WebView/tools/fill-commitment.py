"""
填写《互联网信息服务核准承诺书（个人版）》：身份证号、签署日期、手写签名。
所有文档写操作都通过本地 editor_sdk 的 edsdk.py 完成（实时可视化编辑）。
"""
import argparse
import json
import os
import subprocess
import sys

EDSDK = r'E:\install\workbuddy\resources\app.asar.unpacked\resources\plugins\workbuddy-builtin\skills\workbuddy-local-office-edit\edsdk.py'
ROOT = r'E:\Project\workbuddy\WebView'
DOCX = os.path.join(ROOT, 'output', '互联网信息服务核准承诺书（个人版）-杨东.docx')
SIGN_PNG = os.path.join(ROOT, 'output', '签名-杨东.png')


def call(tool, params):
    cmd = [sys.executable, EDSDK, 'call', tool, '--json',
           json.dumps(params, ensure_ascii=False)]
    proc = subprocess.run(cmd, capture_output=True, text=True, encoding='utf-8')
    out = (proc.stdout or '').strip()
    err = (proc.stderr or '').strip()
    if proc.returncode != 0:
        raise RuntimeError(tool + ' 调用失败: ' + (err or out))
    print('[' + tool + '] ' + out[:600])
    try:
        return json.loads(out)
    except json.JSONDecodeError:
        return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--file-id', required=True)
    ap.add_argument('--id-number', default='420921199204205594')
    ap.add_argument('--date', default='2026 年  11  月  6  日')
    ap.add_argument('--img-w', type=int, default=100)
    ap.add_argument('--img-h', type=int, default=59)
    args = ap.parse_args()
    fid = args.file_id

    # 1) 日期：从后往前改，避免影响前面的坐标
    call('doc_find_and_replace', {
        'file_id': fid,
        'old_text': '年     月    日',
        'new_text': args.date,
    })

    # 2) 身份证号码：紧跟「身份证号码：」之后
    found = call('doc_find', {'file_id': fid, 'text': '身份证号码：'})
    loc = found['locations'][0]
    call('doc_insert_text', {
        'file_id': fid,
        'idx': loc['end'],
        'text': args.id_number,
    })

    # 3) 去掉签名行的占位空格，让签名图紧贴「签名：」
    call('doc_find_and_replace', {
        'file_id': fid,
        'old_text': '签名：' + ' ' * 21,
        'new_text': '签名：',
    })

    # 4) 签名行原本是 22pt 固定行距，会裁剪图片；改为最小行距
    found = call('doc_find', {'file_id': fid, 'text': '签名：'})
    pid = found['locations'][0]['paragraph_id']
    call('doc_modify_paragraph', {
        'file_id': fid,
        'paragraph_id': pid,
        'line_spacing_rule': 3,
        'line_spacing': 46,
    })

    # 5) 重新查询（写操作会让坐标漂移），插入手写签名图片
    found = call('doc_find', {'file_id': fid, 'text': '签名：'})
    loc = found['locations'][0]
    call('doc_insert_image', {
        'file_id': fid,
        'idx': loc['end'],
        'image_path': SIGN_PNG,
        'w': args.img_w,
        'h': args.img_h,
    })

    # 6) 保存回工作副本
    call('save_file', {'file_id': fid})
    print('完成，文档:', DOCX)


if __name__ == '__main__':
    main()
