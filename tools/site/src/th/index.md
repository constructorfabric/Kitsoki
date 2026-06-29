---
layout: home

hero:
  name: kitsoki
  text: เวิร์กโฟลว์ LLM แบบ deterministic
  tagline: เอนจินเวิร์กโฟลว์แบบสนทนา เวิร์กโฟลว์ของคุณเป็น YAML state machine ที่ตรวจสอบได้ และ LLM ทำงานเฉพาะจุดตัดสินใจที่แคบ ระบุชื่อได้ และตามรอยได้
  image:
    src: /branding/mesa-sun.svg
    alt: kitsoki mesa sun
  actions:
    - theme: brand
      text: เริ่มต้น
      link: /guide/getting-started
    - theme: alt
      text: สำรวจฟีเจอร์
      link: /th/features/
---

<HeroDemo />

## ทำไมต้อง kitsoki

**Deterministic ก่อนเสมอ** runtime คือกราฟของ room ที่คุณเขียนด้วย YAML ทุก transition, guard และ effect replay ได้เหมือนเดิมทุกครั้ง ส่วน LLM ถูกจำกัดไว้ที่จุดตัดสินใจที่ประกาศไว้และบันทึกใน trace ที่ replay ได้

**story เดียว ใช้ได้ทุก surface** `app.yaml` เดียวขับทั้ง terminal UI, web surface, Jira comments และ daemon แบบ headless จะขับโดยมนุษย์ ส่งต่อให้ agent หรือ replay จาก cassette ก็ยังเป็น state machine เดิม

**ทดสอบได้เหมือนซอฟต์แวร์ เพราะมันคือซอฟต์แวร์** flow fixture replay บทสนทนาทั้งชุดโดยไม่มีค่า LLM, cassette ล็อกคำตอบ agent แบบ byte-for-byte และเดโมบนไซต์นี้ถูกบันทึกจาก deterministic run ชุดเดียวกัน

## ฟีเจอร์

การ์ดด้านล่างสร้างจาก feature catalog เดียวกับทัวร์ในแอป เดโมที่บันทึกไว้ และ scenario สำหรับ QA

<FeatureGrid :kinds="['feature', 'product-tour']" :promo-only="true" />
